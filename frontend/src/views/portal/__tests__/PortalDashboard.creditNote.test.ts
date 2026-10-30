import { describe, it, expect, vi } from 'vitest'
import { ref } from 'vue'
import { mountWithPlugins } from '@/test/test-utils'
import PortalDashboard from '@/views/portal/PortalDashboard.vue'
import type { MemberInvoice } from '@/composables/useMyInvoices'

// A credit note asks for nothing, so the dashboard's invoice chips show no
// due date for it; a faktura still does.

function invoice(overrides: Partial<MemberInvoice>): MemberInvoice {
  return {
    id: 'inv',
    invoice_number: 70,
    kid_number: '1234567890',
    total_amount: 1500,
    issue_date: '2026-01-01',
    due_date: '2999-01-01',
    sent_at: '2026-01-01T00:00:00Z',
    paid: false,
    payment_received: false,
    credit_note: false,
    credited: false,
    owed: true,
    price_item_name: 'Havneplass',
    description: '',
    has_pdf: true,
    ...overrides,
  }
}

const invoices = [
  invoice({ id: 'faktura', invoice_number: 70 }),
  invoice({ id: 'cn', invoice_number: 71, total_amount: -5, credit_note: true, owed: false, price_item_name: 'Kreditert avgift' }),
]

vi.mock('@/composables/useMyInvoices', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/composables/useMyInvoices')>()
  return {
    ...actual,
    useMyInvoices: () => ({
      unpaid: ref(invoices.filter((i) => i.owed)),
      paid: ref(invoices.filter((i) => !i.owed)),
      isLoading: ref(false),
    }),
  }
})
vi.mock('@/composables/useFeatures', () => ({ useFeatures: () => ({ isEnabled: () => true }) }))
vi.mock('@/composables/usePaymentDataUpdatedAt', () => ({ usePaymentDataUpdatedAt: () => ({ updatedAt: ref(null) }) }))
vi.mock('@/lib/apiClient', () => ({
  useApiClient: () => ({ GET: vi.fn() }),
  unwrap: (v: unknown) => v,
}))
vi.mock('@tanstack/vue-query', () => ({
  useQuery: () => ({ data: ref(undefined), isLoading: ref(false), isError: ref(false) }),
  useMutation: () => ({ mutate: vi.fn(), isPending: ref(false) }),
}))

describe('PortalDashboard invoice chips', () => {
  it('shows a due date for a faktura but not for a credit note', () => {
    const w = mountWithPlugins(PortalDashboard, { global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    const chips = w.findAll('a').filter((a) => a.text().includes('#70') || a.text().includes('#71'))
    expect(chips).toHaveLength(2)
    const [faktura, creditNote] = chips
    expect(faktura.text()).toContain('portal.invoices.due')
    expect(creditNote.text()).toContain('#71')
    expect(creditNote.text()).not.toContain('portal.invoices.due')
    expect(creditNote.text()).not.toContain('1234567890')
  })
})
