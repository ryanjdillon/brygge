import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { ref } from 'vue'
import { flushPromises } from '@vue/test-utils'
import type { Router } from 'vue-router'
import { mountWithPlugins } from '@/test/test-utils'
import FakturaView from '@/views/admin/FakturaView.vue'

const { areas } = vi.hoisted(() => ({ areas: { writable: ['money'] as string[] } }))

vi.mock('@/composables/useCan', () => ({ useCan: () => ({ can: (area: string) => areas.writable.includes(area) }) }))
vi.mock('@/composables/useFreshTotp', () => ({ useFreshTotp: () => ({ ensureFreshTotp: vi.fn() }) }))
vi.mock('@/composables/usePaymentDataUpdatedAt', () => ({
  usePaymentDataUpdatedAt: () => ({ updatedAt: ref(null) }),
}))

const ALL_TABS = ['create', 'drafts', 'voided', 'sent', 'archive', 'imported']

// The app router is ready before an admin view mounts; the test router is
// not, so navigate once mounted to land on the URL under test.
async function mountView(url: string) {
  const wrapper = mountWithPlugins(FakturaView, {
    global: {
      stubs: {
        FakturaList: true,
        SingleFakturaModal: true,
        GroupFakturaTab: true,
        PdfArkivView: true,
        LastUpdated: true,
      },
    },
  })
  const router = wrapper.vm.$router as Router
  await router.isReady()
  await router.push(url)
  await flushPromises()
  return { wrapper, router }
}

function tabLinks(wrapper: Awaited<ReturnType<typeof mountView>>['wrapper']) {
  return wrapper.findAll('nav a')
}

describe('FakturaView tabs', () => {
  beforeEach(() => {
    areas.writable = ['money']
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({ ok: true, json: () => Promise.resolve({ count: 3 }) }),
    )
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('renders each tab as a link to its ?tab= URL, keeping other query params', async () => {
    const { wrapper } = await mountView('/admin?tab=sent&pay=paid')
    const links = tabLinks(wrapper)
    expect(links).toHaveLength(ALL_TABS.length)
    links.forEach((link, i) => {
      const href = link.attributes('href')!
      expect(href).toContain(`tab=${ALL_TABS[i]}`)
      expect(href).toContain('pay=paid')
      expect(link.attributes('type')).toBeUndefined()
    })
  })

  it('marks only the active tab with aria-current and active styling', async () => {
    const { wrapper } = await mountView('/admin?tab=sent')
    const current = tabLinks(wrapper).filter((l) => l.attributes('aria-current') === 'page')
    expect(current).toHaveLength(1)
    expect(current[0].attributes('href')).toContain('tab=sent')
    expect(current[0].classes()).toContain('border-brand-600')
    const drafts = tabLinks(wrapper).find((l) => l.attributes('href')!.includes('tab=drafts'))!
    expect(drafts.classes()).not.toContain('border-brand-600')
  })

  it('pushes a history entry when a tab is clicked', async () => {
    const { wrapper, router } = await mountView('/admin?tab=sent')
    const push = vi.spyOn(router, 'push')
    await tabLinks(wrapper).find((l) => l.attributes('href')!.includes('tab=drafts'))!.trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.tab).toBe('drafts')
    expect(push).toHaveBeenCalled()
    expect(tabLinks(wrapper).find((l) => l.attributes('aria-current') === 'page')!.attributes('href')).toContain(
      'tab=drafts',
    )
  })

  it('sends a bare URL to the default tab', async () => {
    const { router } = await mountView('/admin')
    expect(router.currentRoute.value.query.tab).toBe('create')
  })

  it('hides the create tab from readers without the money role', async () => {
    areas.writable = []
    const { wrapper, router } = await mountView('/admin')
    expect(router.currentRoute.value.query.tab).toBe('sent')
    expect(tabLinks(wrapper).some((l) => l.attributes('href')!.includes('tab=create'))).toBe(false)
  })
})
