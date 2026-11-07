import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import JoinView from '@/views/JoinView.vue'

const client = { GET: vi.fn(), POST: vi.fn() }

vi.mock('@/lib/apiClient', async () => {
  const actual = await vi.importActual<typeof import('@/lib/apiClient')>('@/lib/apiClient')
  return { useApiClient: () => client, unwrap: actual.unwrap, ApiError: actual.ApiError }
})

function ok(data: unknown, status = 200) {
  return { data, response: new Response(null, { status }) }
}
function fail(status: number, error: unknown) {
  return { error, response: new Response(null, { status }) }
}

const OPEN = { open: true, price: { amount: 650, currency: 'NOK', name: 'Kontingent', description: 'Kontingent' }, missing: [] }
const VERSIONS: Record<string, string> = { statutes: 's-1', harbour_rules: 'h-1', privacy: 'p-1', terms_of_sale: 't-1' }

// Routes GET calls by path; `legal` lets a test change the document versions.
function serve(status: unknown = OPEN, legal: Record<string, string> = VERSIONS) {
  client.GET.mockImplementation(async (path: string, opts?: { params: { path: { docType: string } } }) => {
    if (path === '/api/v1/join/status') return ok(status)
    if (path === '/api/v1/legal/{docType}') {
      const doc = opts!.params.path.docType
      return legal[doc] ? ok({ version: legal[doc], content: '', doc_type: doc, id: doc, published_at: '' }) : fail(404, { error: 'document not found' })
    }
    throw new Error('unexpected GET ' + path)
  })
}

async function mountForm(user: { roles: string[] } | null = null) {
  // The auth store loads the session on creation; keep that request pending
  // so the user given here stands.
  vi.spyOn(globalThis, 'fetch').mockImplementation(() => new Promise(() => {}))
  const wrapper = mountWithPlugins(JoinView, {
    piniaOptions: { initialState: { auth: { user } } },
    attachTo: document.body,
  })
  await flushPromises()
  return wrapper
}

async function fillValid(wrapper: Awaited<ReturnType<typeof mountForm>>) {
  await wrapper.find('#join-first-name').setValue('Kari')
  await wrapper.find('#join-last-name').setValue('Testesen')
  await wrapper.find('#join-email').setValue('kari@example.com')
  await wrapper.find('#join-postal-code').setValue('0001')
  await wrapper.find('#join-city').setValue('Testby')
  await wrapper.find('#join-local-no').setValue(true)
  await wrapper.find('#join-accept').setValue(true)
}

describe('JoinView', () => {
  beforeEach(() => {
    client.GET.mockReset()
    client.POST.mockReset()
    vi.restoreAllMocks()
  })

  it('shows the short form with the price and no boat fields', async () => {
    serve()
    const wrapper = await mountForm()
    expect(wrapper.find('[data-testid="join-form"]').exists()).toBe(true)
    for (const id of ['first-name', 'last-name', 'email', 'phone', 'address', 'postal-code', 'city']) {
      expect(wrapper.find(`#join-${id}`).exists()).toBe(true)
    }
    expect(wrapper.find('#join-boat-name').exists()).toBe(false)
    expect(wrapper.find('[data-testid="join-price"]').text()).toContain('join.form.price')
    expect(wrapper.find('#join-newsletter').element).toHaveProperty('checked', false)
  })

  it('links each legal document in a new tab', async () => {
    serve()
    const wrapper = await mountForm()
    for (const doc of Object.keys(VERSIONS)) {
      const a = wrapper.find(`[data-testid="join-doc-${doc}"]`)
      expect(a.attributes('href')).toBe(`/vilkar/${doc}`)
      expect(a.attributes('target')).toBe('_blank')
      expect(a.attributes('rel')).toBe('noopener noreferrer')
    }
  })

  it('hides the honeypot from people and assistive tech', async () => {
    serve()
    const wrapper = await mountForm()
    const hp = wrapper.find('#join-website')
    expect(hp.attributes('name')).toBe('website')
    expect(hp.attributes('tabindex')).toBe('-1')
    expect(hp.attributes('autocomplete')).toBe('new-password')
    expect(hp.attributes('data-1p-ignore')).toBeDefined()
    expect(hp.attributes('data-lpignore')).toBe('true')
    expect(hp.element.closest('[aria-hidden="true"]')).not.toBeNull()
  })

  it('refuses to submit until the required fields, the local answer and the terms are given', async () => {
    serve()
    const wrapper = await mountForm()
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(client.POST).not.toHaveBeenCalled()
    const text = wrapper.text()
    expect(text).toContain('join.form.required')
    expect(text).toContain('join.form.chooseOne')
    expect(text).toContain('join.form.acceptRequired')

    await wrapper.find('#join-email').setValue('not-an-email')
    await wrapper.find('form').trigger('submit')
    expect(wrapper.text()).toContain('join.form.invalidEmail')
  })

  it('focuses the first invalid field and announces a summary', async () => {
    serve()
    const wrapper = await mountForm()
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.find('[data-testid="join-error-summary"]').attributes('role')).toBe('alert')
    expect(document.activeElement?.id).toBe('join-first-name')

    await wrapper.find('#join-first-name').setValue('Kari')
    await wrapper.find('#join-last-name').setValue('Testesen')
    await wrapper.find('#join-email').setValue('kari@example.com')
    await wrapper.find('#join-postal-code').setValue('0001')
    await wrapper.find('#join-city').setValue('Testby')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    // The yes/no group is next: its first option takes focus.
    expect(document.activeElement?.id).toBe('join-local-yes')
    wrapper.unmount()
  })

  it('ties each error to its field for assistive tech', async () => {
    serve()
    const wrapper = await mountForm()
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    for (const id of ['join-first-name', 'join-last-name', 'join-email', 'join-postal-code', 'join-city']) {
      const input = wrapper.find(`#${id}`)
      expect(input.attributes('aria-invalid')).toBe('true')
      expect(input.attributes('aria-describedby')).toBe(`${id}-error`)
      expect(wrapper.find(`#${id}-error`).text()).toBe('join.form.required')
    }
    const group = wrapper.find('fieldset')
    expect(group.attributes('aria-invalid')).toBe('true')
    expect(wrapper.find(`#${group.attributes('aria-describedby')}`).text()).toBe('join.form.chooseOne')
    const accept = wrapper.find('#join-accept')
    expect(accept.attributes('aria-invalid')).toBe('true')
    expect(wrapper.find(`#${accept.attributes('aria-describedby')}`).text()).toBe('join.form.acceptRequired')
    wrapper.unmount()
  })

  it('labels the price with the server year', async () => {
    serve({ ...OPEN, year: 2031 })
    const wrapper = await mountForm()
    // The test t() fills params into the key only where it names them, so
    // check the value the label is given instead.
    expect((wrapper.vm as unknown as { year: number }).year).toBe(2031)
    wrapper.unmount()
  })

  it('submits the payload with consent versions and the honeypot, then asks to check email', async () => {
    serve()
    client.POST.mockResolvedValue(ok({ status: 'received' }, 202))
    const wrapper = await mountForm()
    await fillValid(wrapper)
    await wrapper.find('#join-phone').setValue('+4700000001')
    await wrapper.find('#join-newsletter').setValue(true)
    await wrapper.find('#join-website').setValue('http://spam.example')
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(client.POST).toHaveBeenCalledWith('/api/v1/join', {
      body: {
        first_name: 'Kari',
        last_name: 'Testesen',
        email: 'kari@example.com',
        phone: '+4700000001',
        address_line: undefined,
        postal_code: '0001',
        city: 'Testby',
        is_local: false,
        newsletter: true,
        accept_terms: true,
        consent_versions: VERSIONS,
        website: 'http://spam.example',
      },
    })
    expect(wrapper.find('[data-testid="join-sent"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('join.sent.title')
  })

  it('leaves consent_versions out when a document did not load', async () => {
    serve(OPEN, { ...VERSIONS, privacy: '' })
    client.POST.mockResolvedValue(ok({ status: 'received' }, 202))
    const wrapper = await mountForm()
    await fillValid(wrapper)
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(client.POST.mock.calls[0][1].body.consent_versions).toBeUndefined()
  })

  it('shows the closed state with a contact link', async () => {
    serve({ open: false, price: null, missing: ['price_item_unset'] })
    const wrapper = await mountForm()
    const closed = wrapper.find('[data-testid="join-closed"]')
    expect(closed.exists()).toBe(true)
    expect(closed.text()).toContain('join.closed.title')
    expect(closed.find('a').attributes('href')).toBe('/contact')
    expect(wrapper.find('form').exists()).toBe(false)
  })

  it('switches to the closed state when the submit answers join_closed', async () => {
    serve()
    client.POST.mockResolvedValue(fail(409, { error: 'joining is not open', code: 'join_closed' }))
    const wrapper = await mountForm()
    await fillValid(wrapper)
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.find('[data-testid="join-closed"]').exists()).toBe(true)
  })

  it('reloads changed terms, clears the tick and resubmits with the new versions', async () => {
    serve()
    client.POST.mockResolvedValueOnce(
      fail(409, { error: 'a document has a newer version', code: 'legal_document_stale', versions: { ...VERSIONS, privacy: 'p-2' } }),
    )
    const wrapper = await mountForm()
    await fillValid(wrapper)
    serve(OPEN, { ...VERSIONS, privacy: 'p-2' })
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(wrapper.find('[data-testid="join-terms-changed"]').exists()).toBe(true)
    expect(wrapper.find('#join-accept').element).toHaveProperty('checked', false)
    expect(wrapper.find('[data-testid="join-sent"]').exists()).toBe(false)

    client.POST.mockResolvedValueOnce(ok({ status: 'received' }, 202))
    await wrapper.find('#join-accept').setValue(true)
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(client.POST).toHaveBeenCalledTimes(2)
    expect(client.POST.mock.calls[1][1].body.consent_versions.privacy).toBe('p-2')
    expect(wrapper.find('[data-testid="join-sent"]').exists()).toBe(true)
  })

  it('shows a retryable message when rate limited', async () => {
    serve()
    client.POST.mockResolvedValue(fail(429, { error: 'too many requests' }))
    const wrapper = await mountForm()
    await fillValid(wrapper)
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.find('[data-testid="join-submit-error"]').text()).toBe('join.form.rateLimited')
  })

  it('tells a signed-in member they already are one instead of showing the form', async () => {
    serve()
    const wrapper = await mountForm({ roles: ['member'] })
    expect(wrapper.find('[data-testid="join-already-member"]').exists()).toBe(true)
    expect(wrapper.find('form').exists()).toBe(false)
    expect(client.GET).not.toHaveBeenCalled()
  })

  it('sends a signed-in applicant on to finish joining', async () => {
    serve()
    const wrapper = await mountForm({ roles: ['applicant'] })
    const box = wrapper.find('[data-testid="join-applicant"]')
    expect(box.exists()).toBe(true)
    expect(box.find('a').attributes('href')).toBe('/bli-medlem/fullfor')
    expect(wrapper.find('form').exists()).toBe(false)
  })
})
