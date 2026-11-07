import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createTestingPinia } from '@pinia/testing'
import { createRouter, createMemoryHistory, RouterView } from 'vue-router'
import LegalDocumentView from '@/views/LegalDocumentView.vue'

const client = { GET: vi.fn() }

// Show interpolated values even for keys whose text the global mock cannot
// fill, so the fallback's cancellation contact is visible to assertions.
vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) =>
      params ? `${key}(${Object.values(params).map(String).join('|')})` : key,
    locale: { value: 'nn' },
  }),
}))

vi.mock('@/lib/apiClient', async () => {
  const actual = await vi.importActual<typeof import('@/lib/apiClient')>('@/lib/apiClient')
  return { useApiClient: () => client, unwrap: actual.unwrap, ApiError: actual.ApiError }
})

const notFound = () => ({ error: { error: 'document not found' }, response: new Response(null, { status: 404 }) })
const published = (docType: string, content: string) => ({
  data: { id: `id-${docType}`, doc_type: docType, version: 'v1', content, published_at: '2026-01-01T00:00:00Z' },
  response: new Response(null, { status: 200 }),
})

async function mountAt(path: string, initialState: Record<string, unknown> = {}) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/vilkar/:docType', component: LegalDocumentView, props: true },
      { path: '/privacy', component: LegalDocumentView, props: { docType: 'privacy' } },
      { path: '/salgsvilkar', component: LegalDocumentView, props: { docType: 'terms_of_sale' } },
      { path: '/portal/privacy-policy', component: LegalDocumentView, props: { docType: 'privacy', embedded: true } },
    ],
  })
  await router.push(path)
  await router.isReady()
  const wrapper = mount(RouterView, { global: { plugins: [createTestingPinia({ createSpy: vi.fn, initialState }), router] } })
  await flushPromises()
  return wrapper
}

describe('LegalDocumentView', () => {
  // Braces matter: a function returned from beforeEach is run as teardown.
  beforeEach(() => {
    client.GET.mockReset()
  })
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('shows the published document', async () => {
    client.GET.mockResolvedValue({
      data: { id: 'd1', doc_type: 'statutes', version: 'v1', content: 'Paragraf 1', published_at: '2026-01-01T00:00:00Z' },
      response: new Response(null, { status: 200 }),
    })
    const wrapper = await mountAt('/vilkar/statutes')
    expect(client.GET).toHaveBeenCalledWith('/api/v1/legal/{docType}', { params: { path: { docType: 'statutes' } }, signal: expect.any(AbortSignal) })
    expect(wrapper.find('h1').text()).toBe('join.legal.statutes')
    expect(wrapper.find('article').classes()).toContain('py-8')
    expect(wrapper.find('[data-testid="legal-content"]').text()).toBe('Paragraf 1')
  })

  it('says when the document is not published', async () => {
    client.GET.mockResolvedValue({ error: { error: 'document not found' }, response: new Response(null, { status: 404 }) })
    const wrapper = await mountAt('/vilkar/privacy')
    expect(wrapper.find('[data-testid="legal-missing"]').exists()).toBe(true)
  })

  it('does not ask the server for an unknown document type', async () => {
    const wrapper = await mountAt('/vilkar/other')
    expect(client.GET).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="legal-missing"]').exists()).toBe(true)
  })

  it('shows the published privacy policy at /privacy', async () => {
    client.GET.mockResolvedValue({
      data: { id: 'd2', doc_type: 'privacy', version: '2026-11', content: 'Behandlingsansvarleg', published_at: '2026-11-01T00:00:00Z' },
      response: new Response(null, { status: 200 }),
    })
    const wrapper = await mountAt('/privacy')
    expect(client.GET).toHaveBeenCalledWith('/api/v1/legal/{docType}', { params: { path: { docType: 'privacy' } }, signal: expect.any(AbortSignal) })
    expect(wrapper.find('h1').text()).toBe('join.legal.privacy')
    expect(wrapper.find('[data-testid="legal-content"]').text()).toBe('Behandlingsansvarleg')
  })

  it('says at /privacy when no privacy policy is published', async () => {
    client.GET.mockResolvedValue({ error: { error: 'document not found' }, response: new Response(null, { status: 404 }) })
    const wrapper = await mountAt('/privacy')
    expect(wrapper.find('[data-testid="legal-missing"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="legal-content"]').exists()).toBe(false)
  })

  it('shows the published terms of sale at /salgsvilkar', async () => {
    client.GET.mockResolvedValue({
      data: { id: 'd3', doc_type: 'terms_of_sale', version: '2026-11', content: 'Klubben sine vilkår', published_at: '2026-11-01T00:00:00Z' },
      response: new Response(null, { status: 200 }),
    })
    const wrapper = await mountAt('/salgsvilkar')
    expect(client.GET).toHaveBeenCalledWith('/api/v1/legal/{docType}', { params: { path: { docType: 'terms_of_sale' } }, signal: expect.any(AbortSignal) })
    expect(wrapper.find('[data-testid="legal-content"]').text()).toBe('Klubben sine vilkår')
    expect(wrapper.find('#parter').exists()).toBe(false)
  })

  it.each([
    ['nothing is published', { error: { error: 'document not found' }, response: new Response(null, { status: 404 }) }],
    ['the request fails', { error: { error: 'boom' }, response: new Response(null, { status: 500 }) }],
  ])('falls back to the built-in terms at /salgsvilkar when %s', async (_case, reply) => {
    client.GET.mockResolvedValue(reply)
    const wrapper = await mountAt('/salgsvilkar')
    expect(wrapper.find('h1').text()).toBe('salgsvilkar.title')
    expect(wrapper.findAll('section').map((s) => s.attributes('id'))).toEqual([
      'parter', 'betaling', 'oppsigelse', 'levering', 'angrerett', 'retur', 'reklamasjon', 'konflikt',
    ])
    expect(wrapper.find('[data-testid="legal-missing"]').exists()).toBe(false)
  })

  it('falls back to the built-in terms at /vilkar/terms_of_sale too', async () => {
    client.GET.mockResolvedValue({ error: { error: 'document not found' }, response: new Response(null, { status: 404 }) })
    const wrapper = await mountAt('/vilkar/terms_of_sale')
    expect(wrapper.find('#oppsigelse').exists()).toBe(true)
    expect(wrapper.find('[data-testid="legal-missing"]').exists()).toBe(false)
  })

  it('shows a spinner while the document loads', async () => {
    client.GET.mockReturnValue(new Promise(() => {}))
    const wrapper = await mountAt('/privacy')
    expect(wrapper.find('[role="status"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="legal-missing"]').exists()).toBe(false)
  })

  it('shows an error, not "not published", when the request fails', async () => {
    client.GET.mockReturnValue(Promise.reject(new TypeError('Failed to fetch')))
    const wrapper = await mountAt('/privacy')
    expect(wrapper.find('[data-testid="legal-error"]').text()).toBe('join.legal.loadFailed')
    expect(wrapper.find('[data-testid="legal-missing"]').exists()).toBe(false)
  })

  it('falls back to the built-in terms on a network error', async () => {
    client.GET.mockReturnValue(Promise.reject(new TypeError('Failed to fetch')))
    const wrapper = await mountAt('/salgsvilkar')
    expect(wrapper.find('#oppsigelse').exists()).toBe(true)
  })

  it.each([
    ['privacy', '/privacy'],
    ['terms_of_sale', '/salgsvilkar'],
  ])('gives up after the timeout (%s)', async (docType, path) => {
    const controller = new AbortController()
    const timeout = vi.spyOn(AbortSignal, 'timeout').mockReturnValue(controller.signal)
    client.GET.mockImplementation(
      (_url: string, opts: { signal: AbortSignal }) =>
        new Promise((_resolve, reject) => opts.signal.addEventListener('abort', () => reject(opts.signal.reason))),
    )
    const wrapper = await mountAt(path)
    expect(timeout).toHaveBeenCalledWith(5000)
    expect(wrapper.find('[role="status"]').exists()).toBe(true)
    controller.abort(new DOMException('timed out', 'TimeoutError'))
    await flushPromises()
    if (docType === 'terms_of_sale') {
      expect(wrapper.find('#oppsigelse').exists()).toBe(true)
    } else {
      expect(wrapper.find('[data-testid="legal-error"]').exists()).toBe(true)
    }
  })

  it.each([
    ['blank content', { data: { id: 'd', doc_type: 'terms_of_sale', version: 'v1', content: '  \n ', published_at: '2026-01-01T00:00:00Z' } }],
    ['no body', { data: undefined }],
  ])('treats a 200 with %s as unpublished', async (_case, body) => {
    client.GET.mockResolvedValue({ ...body, response: new Response(null, { status: 200 }) })
    const terms = await mountAt('/salgsvilkar')
    expect(terms.find('#oppsigelse').exists()).toBe(true)
    client.GET.mockResolvedValue({ ...body, response: new Response(null, { status: 200 }) })
    const privacy = await mountAt('/privacy')
    expect(privacy.find('[data-testid="legal-missing"]').exists()).toBe(true)
  })

  it('ignores a late answer for the document type it has left', async () => {
    let answerStatutes: (v: unknown) => void = () => {}
    client.GET.mockImplementation((_url: string, opts: { params: { path: { docType: string } } }) =>
      opts.params.path.docType === 'statutes'
        ? new Promise((resolve) => { answerStatutes = resolve })
        : Promise.resolve(published('harbour_rules', 'Hamnereglement')),
    )
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/vilkar/:docType', component: LegalDocumentView, props: true }],
    })
    await router.push('/vilkar/statutes')
    await router.isReady()
    const wrapper = mount(RouterView, { global: { plugins: [createTestingPinia({ createSpy: vi.fn }), router] } })
    await flushPromises()
    await router.push('/vilkar/harbour_rules')
    await flushPromises()
    answerStatutes(published('statutes', 'Vedtekter'))
    await flushPromises()
    expect(wrapper.find('h1').text()).toBe('join.legal.harbour_rules')
    expect(wrapper.find('[data-testid="legal-content"]').text()).toBe('Hamnereglement')
  })

  it('names the cancellation email and phone in the built-in terms', async () => {
    client.GET.mockResolvedValue(notFound())
    const wrapper = await mountAt('/salgsvilkar', {
      club: { name: 'Testklubb', treasurerEmail: 'kasserar@example.org', phone: '+47 400 00 000' },
    })
    const text = wrapper.find('#oppsigelse').text()
    expect(text).toContain('kasserar@example.org')
    expect(text).toContain('+47 400 00 000')
  })

  it('points to the contact page when the club has no email or phone', async () => {
    client.GET.mockResolvedValue(notFound())
    const wrapper = await mountAt('/salgsvilkar', { club: { treasurerEmail: '', chairmanEmail: '', domain: '', phone: '' } })
    const text = wrapper.find('#oppsigelse').text()
    expect(text).toContain('salgsvilkar.oppsigelse.contactPageFragment')
    expect(text).not.toContain('@')
  })

  it('drops its own padding inside the portal', async () => {
    client.GET.mockResolvedValue(published('privacy', 'Tekst'))
    const wrapper = await mountAt('/portal/privacy-policy')
    expect(wrapper.find('article').classes()).not.toContain('py-8')
  })
})
