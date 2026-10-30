import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref, defineComponent, h } from 'vue'
import { mountWithPlugins } from '@/test/test-utils'
import AccountingDashboard from '@/views/admin/AccountingDashboard.vue'

type Totals = {
  billed: number
  exempted: number
  received: number
  overdue: number
  outstanding: number
  invoice_count: number
  paid_count: number
  overdue_count: number
  outstanding_count: number
}

const summary = ref<{ items: unknown[]; totals: Totals } | null>(null)
const periods = ref<{ year: number; reference_only: boolean }[]>([])

vi.mock('@/composables/useFinancials', () => ({
  useFinancialSummary: () => ({ data: ref(null), isLoading: ref(false) }),
  usePriceItemSummary: () => ({ data: summary, isLoading: ref(false) }),
  useCashFlow: () => ({ data: ref(null), isLoading: ref(false) }),
  useReservationsByMonth: () => ({ data: ref(null), isLoading: ref(false) }),
}))

vi.mock('@/composables/useAccounting', () => ({
  useAccountsList: () => ({ data: ref([]), isLoading: ref(false) }),
  useFiscalPeriods: () => ({ data: periods, isLoading: ref(false) }),
  useJournalEntries: () => ({ data: ref([]), isLoading: ref(false) }),
}))

vi.mock('@/composables/usePricing', () => ({
  usePricing: () => ({ categoryLabel: (c: string) => c, unitLabel: (u: string) => u }),
}))

vi.mock('@/composables/usePaymentDataUpdatedAt', () => ({
  usePaymentDataUpdatedAt: () => ({ updatedAt: ref(null) }),
}))

// Capture the donut's slices instead of rendering SVG.
let lastSlices: { label: string; value: number }[] = []
const DonutStub = defineComponent({
  props: { slices: { type: Array, default: () => [] }, centerValue: String, centerLabel: String },
  setup(props) {
    return () => {
      lastSlices = props.slices as { label: string; value: number }[]
      return h('div', { 'data-testid': 'donut' })
    }
  },
})

function row(over: Partial<Record<string, unknown>>) {
  return {
    price_item_id: 'p-slip',
    name: 'Plassleie',
    description: 'Plassleie',
    category: 'slip_fee',
    amount: 2600,
    unit: 'year',
    billed: 0,
    exempted: 0,
    received: 0,
    overdue: 0,
    outstanding: 0,
    invoice_count: 0,
    paid_count: 0,
    overdue_count: 0,
    ...over,
  }
}

function mountDashboard() {
  return mountWithPlugins(AccountingDashboard, {
    global: { stubs: { DonutChart: DonutStub, BarChart: true, LastUpdated: true } },
  })
}

describe('AccountingDashboard role exemptions', () => {
  beforeEach(() => {
    lastSlices = []
    periods.value = []
  })

  it('shows the exempted column, card note and donut slice when exemptions exist', () => {
    summary.value = {
      items: [
        row({ billed: 8700, exempted: 5200, outstanding: 3500, invoice_count: 3 }),
      ],
      totals: {
        billed: 8700, exempted: 5200, received: 0, overdue: 0, outstanding: 3500,
        invoice_count: 3, paid_count: 0, overdue_count: 0, outstanding_count: 2,
      },
    }
    const wrapper = mountDashboard()

    expect(wrapper.text()).toContain('admin.financials.exempted')
    expect(wrapper.find('[data-testid="billed-exempted"]').exists()).toBe(true)

    const exempt = lastSlices.find((s) => s.label === 'admin.financials.statusExempted')
    expect(exempt?.value).toBe(5200)
    // The slices add up to the gross billed shown in the donut's centre.
    expect(lastSlices.reduce((sum, s) => sum + s.value, 0)).toBe(8700)
  })

  it('renders exactly as before when nothing is exempted', () => {
    summary.value = {
      items: [row({ billed: 2600, received: 2600, invoice_count: 1, paid_count: 1 })],
      totals: {
        billed: 2600, exempted: 0, received: 2600, overdue: 0, outstanding: 0,
        invoice_count: 1, paid_count: 1, overdue_count: 0, outstanding_count: 0,
      },
    }
    const wrapper = mountDashboard()

    expect(wrapper.text()).not.toContain('admin.financials.exempted')
    expect(wrapper.find('[data-testid="billed-exempted"]').exists()).toBe(false)
    expect(lastSlices.map((s) => s.label)).not.toContain('admin.financials.statusExempted')
    expect(lastSlices).toHaveLength(3)
  })
})

describe('AccountingDashboard reference years', () => {
  beforeEach(() => {
    summary.value = null
  })

  it('banners the selected year when it is a reference year', () => {
    periods.value = [{ year: new Date().getFullYear(), reference_only: true }]
    expect(mountDashboard().get('[data-testid="reference-banner"]').text()).toContain(
      'admin.accounting.reports.referenceBanner',
    )
  })

  it('shows no banner for an ordinary year', () => {
    periods.value = [{ year: new Date().getFullYear(), reference_only: false }]
    expect(mountDashboard().find('[data-testid="reference-banner"]').exists()).toBe(false)
  })
})
