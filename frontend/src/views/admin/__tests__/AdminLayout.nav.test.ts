import { describe, it, expect, vi } from 'vitest'
import { defineComponent, h, ref, type PropType } from 'vue'
import { mountWithPlugins } from '@/test/test-utils'
import type { NavGroup } from '@/components/layout/navTypes'
import AdminLayout from '@/views/admin/AdminLayout.vue'

const { roles, accountingOn } = vi.hoisted(() => ({
  roles: { value: [] as string[] },
  accountingOn: { value: true },
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ hasRole: (r: string) => roles.value.includes(r) }),
}))
vi.mock('@/stores/club', () => ({ useClubStore: () => ({ ensureLoaded: vi.fn() }) }))
vi.mock('@/stores/breadcrumb', () => ({ useBreadcrumbStore: () => ({ setPage: vi.fn() }) }))
vi.mock('@/composables/useFeatures', () => ({
  useFeatures: () => ({ isEnabled: (f: string) => (f === 'accounting' ? accountingOn.value : true) }),
}))
vi.mock('@/composables/useNavGate', () => ({ useNavGate: () => ({ gateToFresh: vi.fn() }) }))
vi.mock('@/composables/useBankReconcile', () => ({
  useBankUnmatchedCount: () => ({ data: ref(0) }),
  usePendingRefundCount: () => ({ data: ref(0) }),
}))

// Renders what the layout hands the sidebar: one block per group, with the
// route of each item it kept.
const SidebarStub = defineComponent({
  props: { groups: { type: Array as PropType<NavGroup[]>, required: true } },
  setup(props) {
    return () =>
      props.groups.map((g) =>
        h(
          'section',
          { 'data-group': g.titleKey ?? '' },
          g.items.map((i) => h('a', { 'data-to': i.to }, i.label)),
        ),
      )
  },
})

function navFor(userRoles: string[], accounting = true) {
  roles.value = userRoles
  accountingOn.value = accounting
  const wrapper = mountWithPlugins(AdminLayout, {
    global: {
      stubs: { SidebarNav: SidebarStub, RouterView: true, ErrorBoundary: true, FeedbackWidget: true },
    },
  })
  const group = (key: string) =>
    wrapper.findAll(`[data-group="${key}"] [data-to]`).map((a) => a.attributes('data-to'))
  return { group }
}

const PAYMENT_PAGES = ['/admin/harbor/recent-payments', '/admin/harbor/payment-targets']

describe('AdminLayout payment navigation', () => {
  it('lists the payments page and the QR codes under Economy, not Harbor', () => {
    const { group } = navFor(['treasurer'])
    expect(group('admin.groupEconomy')).toEqual(expect.arrayContaining(PAYMENT_PAGES))
    for (const to of PAYMENT_PAGES) expect(group('admin.groupHarbor')).not.toContain(to)
  })

  it('keeps the pair visible to a harbor master whatever the accounting flag says', () => {
    const { group } = navFor(['harbor_master'], false)
    expect(group('admin.groupEconomy')).toEqual(PAYMENT_PAGES)
  })

  it('keeps the existing URLs', () => {
    const { group } = navFor(['admin'])
    expect(group('admin.groupEconomy').slice(0, 2)).toEqual(PAYMENT_PAGES)
  })
})
