import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import { setJoinStatus, refetchJoinStatus } from '@/test/joinStatus'
import JoinView from '@/views/JoinView.vue'

vi.mock('@/composables/useJoinStatus', async () => (await import('@/test/joinStatus')).joinStatusModule)

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

const FIKTIV = {
  id: 'v:4601:90001:12:',
  label: 'Fiktivvegen 12, 5999 Testby',
  street: 'Fiktivvegen 12',
  postal_code: '5999',
  city: 'Testby',
  kommunenummer: '4601',
}

// The address search's answer; a test may replace it.
let addressSearch: () => unknown = () => ok({ addresses: [FIKTIV], fallback: false })

type Status = { open: boolean; price: typeof OPEN.price | null; year?: number; missing?: string[] }

// Sets the join status and routes GET calls by path; `legal` lets a test
// change the document versions.
function serve(status: Status = OPEN, legal: Record<string, string> = VERSIONS) {
  setJoinStatus(status)
  client.GET.mockImplementation(async (path: string, opts?: { params: { path: { docType: string } } }) => {
    if (path === '/api/v1/join/address-search') return addressSearch()
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

type Wrapper = Awaited<ReturnType<typeof mountForm>>

async function fillPerson(wrapper: Wrapper) {
  await wrapper.find('#join-first-name').setValue('Kari')
  await wrapper.find('#join-last-name').setValue('Testesen')
  await wrapper.find('#join-email').setValue('kari@example.com')
  await wrapper.find('#join-phone').setValue('400 00 001')
  await wrapper.find('#join-accept').setValue(true)
}

// Fills every required field, with the address typed by hand.
async function fillValid(wrapper: Wrapper) {
  await fillPerson(wrapper)
  await wrapper.find('[data-testid="join-address-manual"]').trigger('click')
  await wrapper.find('#join-address').setValue('Testvegen 1')
  await wrapper.find('#join-postal-code').setValue('0001')
  await wrapper.find('#join-city').setValue('Testby')
}

// Types into the address search and waits out the debounce. Only the timers
// are faked: a faked clock would date the listeners Vue attaches after the
// search later than the events a test then fires, and Vue drops those.
async function typeAddress(wrapper: Wrapper, text: string) {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  try {
    await wrapper.find('#join-address-search').setValue(text)
    await vi.advanceTimersByTimeAsync(300)
  } finally {
    vi.useRealTimers()
  }
  await flushPromises()
}

describe('JoinView', () => {
  beforeEach(() => {
    client.GET.mockReset()
    client.POST.mockReset()
    vi.restoreAllMocks()
    addressSearch = () => ok({ addresses: [FIKTIV], fallback: false })
  })

  it('shows the short form with the price, an address search and no boat or residency fields', async () => {
    serve()
    const wrapper = await mountForm()
    expect(wrapper.find('[data-testid="join-form"]').exists()).toBe(true)
    for (const id of ['first-name', 'last-name', 'email', 'phone', 'address-search']) {
      expect(wrapper.find(`#join-${id}`).exists()).toBe(true)
    }
    expect(wrapper.find('#join-boat-name').exists()).toBe(false)
    // Residency follows from the address; it is never asked.
    expect(wrapper.find('input[name="is_local"]').exists()).toBe(false)
    expect(wrapper.find('[role="radiogroup"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('join.form.isLocal')
    expect(wrapper.find('[data-testid="address-attribution"]').text()).toBe('join.form.address.attribution')
    const phone = wrapper.find('#join-phone')
    expect(phone.attributes('inputmode')).toBe('tel')
    expect(phone.attributes('autocomplete')).toBe('tel')
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

  it('refuses to submit until the required fields, an address and the terms are given', async () => {
    serve()
    const wrapper = await mountForm()
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(client.POST).not.toHaveBeenCalled()
    const text = wrapper.text()
    expect(text).toContain('join.form.required')
    expect(text).toContain('join.form.address.pickRequired')
    expect(text).toContain('join.form.acceptRequired')
    expect(wrapper.find('#join-phone-error').text()).toBe('join.form.required')

    await wrapper.find('#join-email').setValue('not-an-email')
    await wrapper.find('form').trigger('submit')
    expect(wrapper.text()).toContain('join.form.invalidEmail')
  })

  it('requires a plausible phone number, as the server does', async () => {
    serve()
    const wrapper = await mountForm()
    await fillValid(wrapper)
    for (const bad of ['12345', '0000 0000', '+47 400 00', 'tel:40000001']) {
      await wrapper.find('#join-phone').setValue(bad)
      await wrapper.find('form').trigger('submit')
      await flushPromises()
      expect(wrapper.find('#join-phone-error').text()).toBe('join.form.invalidPhone')
    }
    expect(client.POST).not.toHaveBeenCalled()
  })

  it('requires every manual address field', async () => {
    serve()
    const wrapper = await mountForm()
    await fillPerson(wrapper)
    await wrapper.find('[data-testid="join-address-manual"]').trigger('click')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    for (const id of ['join-address', 'join-postal-code', 'join-city']) {
      expect(wrapper.find(`#${id}-error`).text()).toBe('join.form.required')
    }
    expect(client.POST).not.toHaveBeenCalled()
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
    await wrapper.find('#join-phone').setValue('40000001')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    // The address search is next.
    expect(document.activeElement?.id).toBe('join-address-search')
    wrapper.unmount()
  })

  it('ties each error to its field for assistive tech', async () => {
    serve()
    const wrapper = await mountForm()
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    for (const id of ['join-first-name', 'join-last-name', 'join-email', 'join-phone']) {
      const input = wrapper.find(`#${id}`)
      expect(input.attributes('aria-invalid')).toBe('true')
      expect(input.attributes('aria-describedby')).toBe(`${id}-error`)
      expect(wrapper.find(`#${id}-error`).text()).toBe('join.form.required')
    }
    const search = wrapper.find('#join-address-search')
    expect(search.attributes('aria-invalid')).toBe('true')
    expect(search.attributes('aria-describedby')?.split(' ')).toEqual(['join-address-search-error', 'join-address-helper'])
    expect(wrapper.find('#join-address-search-error').text()).toBe('join.form.address.pickRequired')
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

  it('submits a picked address by its register id, without any residency answer', async () => {
    serve()
    client.POST.mockResolvedValue(ok({ status: 'received' }, 202))
    const wrapper = await mountForm()
    await fillPerson(wrapper)
    await typeAddress(wrapper, 'fiktivv')
    await wrapper.find('[role="option"]').trigger('mousedown')
    await flushPromises()
    // Focus moves to the change button once the search is gone.
    expect(document.activeElement?.id).toBe('join-address-change')
    const picked = wrapper.find('[data-testid="join-address-picked"]')
    expect(picked.text()).toContain('Fiktivvegen 12')
    expect(picked.text()).toContain('5999 Testby')
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    const body = client.POST.mock.calls[0][1].body
    expect(body).toMatchObject({
      phone: '+4740000001',
      address_id: 'v:4601:90001:12:',
      address_line: 'Fiktivvegen 12',
      postal_code: '5999',
      city: 'Testby',
    })
    expect(body).not.toHaveProperty('is_local')
    expect(wrapper.find('[data-testid="join-sent"]').exists()).toBe(true)
  })

  it('changes a picked address back to the search', async () => {
    serve()
    const wrapper = await mountForm()
    await typeAddress(wrapper, 'fiktivv')
    await wrapper.find('[role="option"]').trigger('mousedown')
    await wrapper.find('[data-testid="join-address-change"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="join-address-picked"]').exists()).toBe(false)
    expect(document.activeElement?.id).toBe('join-address-search')
    wrapper.unmount()
  })

  it('switches to manual entry by the link, and back to the search', async () => {
    serve()
    const wrapper = await mountForm()
    await wrapper.find('[data-testid="join-address-manual"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('#join-address-search').exists()).toBe(false)
    expect(wrapper.find('[data-testid="join-address-unavailable"]').exists()).toBe(false)
    for (const id of ['address', 'postal-code', 'city']) expect(wrapper.find(`#join-${id}`).exists()).toBe(true)
    await wrapper.find('[data-testid="join-address-search-again"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('#join-address-search').exists()).toBe(true)
  })

  it.each([
    ['the search errors', () => fail(500, { error: 'internal error' })],
    ['the register is unavailable', () => ok({ addresses: [], fallback: true })],
  ])('switches to manual entry with a notice when %s', async (_, answer) => {
    serve()
    addressSearch = answer
    client.POST.mockResolvedValue(ok({ status: 'received' }, 202))
    const wrapper = await mountForm()
    await fillPerson(wrapper)
    await typeAddress(wrapper, 'fiktivv')
    expect(wrapper.find('[data-testid="join-address-unavailable"]').attributes('role')).toBe('status')
    // The search may be back later: the way back to it stays.
    expect(wrapper.find('[data-testid="join-address-search-again"]').exists()).toBe(true)
    await wrapper.find('#join-address').setValue('Testvegen 1')
    await wrapper.find('#join-postal-code').setValue('0001')
    await wrapper.find('#join-city').setValue('Testby')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    const body = client.POST.mock.calls[0][1].body
    expect(body).toMatchObject({ address_line: 'Testvegen 1', postal_code: '0001', city: 'Testby' })
    expect(body).not.toHaveProperty('address_id')
  })

  it('submits the payload with consent versions and the honeypot, then asks to check email', async () => {
    serve()
    client.POST.mockResolvedValue(ok({ status: 'received' }, 202))
    const wrapper = await mountForm()
    await fillValid(wrapper)
    await wrapper.find('#join-phone').setValue('+47 400 00 001')
    await wrapper.find('#join-newsletter').setValue(true)
    await wrapper.find('#join-website').setValue('http://spam.example')
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(client.POST).toHaveBeenCalledWith('/api/v1/join', {
      body: {
        first_name: 'Kari',
        last_name: 'Testesen',
        email: 'kari@example.com',
        phone: '+4740000001',
        address_line: 'Testvegen 1',
        postal_code: '0001',
        city: 'Testby',
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
    serve({ open: false, price: null })
    const wrapper = await mountForm()
    const closed = wrapper.find('[data-testid="join-closed"]')
    expect(closed.exists()).toBe(true)
    expect(closed.text()).toContain('join.closed.body')
    expect(closed.find('a').attributes('href')).toBe('/contact')
    expect(wrapper.find('a[href^="mailto:"]').exists()).toBe(false)
    expect(wrapper.find('form').exists()).toBe(false)
    expect(client.GET).not.toHaveBeenCalled()
  })

  it('offers a retry when the status cannot be loaded', async () => {
    serve()
    setJoinStatus({ isError: true })
    const wrapper = await mountForm()
    const box = wrapper.find('[data-testid="join-error"]')
    expect(box.exists()).toBe(true)
    await box.find('button').trigger('click')
    expect(refetchJoinStatus).toHaveBeenCalled()
  })

  it('disables the retry and shows it busy while the status is being fetched again', async () => {
    serve()
    setJoinStatus({ isError: true, isFetching: true })
    const wrapper = await mountForm()
    const button = wrapper.find('[data-testid="join-retry"]')
    expect(button.attributes('disabled')).toBeDefined()
    expect(button.attributes('aria-busy')).toBe('true')
    expect(button.find('.animate-spin').exists()).toBe(true)
    await button.trigger('click')
    expect(refetchJoinStatus).not.toHaveBeenCalled()
  })

  it('switches to the closed state when the submit answers join_closed', async () => {
    serve()
    client.POST.mockResolvedValue(fail(409, { error: 'joining is not open', code: 'join_closed' }))
    const wrapper = await mountForm()
    await fillValid(wrapper)
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.find('[data-testid="join-closed"]').exists()).toBe(true)
    expect(refetchJoinStatus).toHaveBeenCalled()
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
    expect(box.find('a').attributes('href')).toBe('/join/complete')
    expect(wrapper.find('form').exists()).toBe(false)
  })
})
