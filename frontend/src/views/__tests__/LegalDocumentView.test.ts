import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createTestingPinia } from '@pinia/testing'
import { createRouter, createMemoryHistory } from 'vue-router'
import LegalDocumentView from '@/views/LegalDocumentView.vue'

const client = { GET: vi.fn() }

vi.mock('@/lib/apiClient', async () => {
  const actual = await vi.importActual<typeof import('@/lib/apiClient')>('@/lib/apiClient')
  return { useApiClient: () => client, unwrap: actual.unwrap, ApiError: actual.ApiError }
})

async function mountAt(path: string) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/vilkar/:docType', component: LegalDocumentView }],
  })
  await router.push(path)
  await router.isReady()
  const wrapper = mount(LegalDocumentView, { global: { plugins: [createTestingPinia({ createSpy: vi.fn }), router] } })
  await flushPromises()
  return wrapper
}

describe('LegalDocumentView', () => {
  beforeEach(() => client.GET.mockReset())

  it('shows the published document', async () => {
    client.GET.mockResolvedValue({
      data: { id: 'd1', doc_type: 'statutes', version: 'v1', content: 'Paragraf 1', published_at: '2026-01-01T00:00:00Z' },
      response: new Response(null, { status: 200 }),
    })
    const wrapper = await mountAt('/vilkar/statutes')
    expect(client.GET).toHaveBeenCalledWith('/api/v1/legal/{docType}', { params: { path: { docType: 'statutes' } } })
    expect(wrapper.find('h1').text()).toBe('join.legal.statutes')
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
})
