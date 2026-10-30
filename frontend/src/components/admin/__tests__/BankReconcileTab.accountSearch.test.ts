import { describe, it, expect, vi } from 'vitest'
import { computed, ref, type Ref } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import BankReconcileTab from '@/components/admin/BankReconcileTab.vue'
import type { BankRowSummary, BankRowSuggestions, VippsCharge } from '@/composables/useBankReconcile'

// One synthetic incoming bank row and one Vipps charge of a slipping price.
const { icon, rows, charges, chargeSuggestions } = vi.hoisted(() => ({
  icon: { template: '<span />' },
  rows: [
    {
      id: 'row1',
      row_date: '2026-09-01',
      amount: 1600,
      kid_number: '',
      reference: 'SYN1',
      counterpart: 'Ola Testmann',
      description: 'Synthetic deposit',
      bank_account_code: '1920',
      likely_duplicate_of_matched: false,
      possible_double_payment: false,
      suggestion_count: 1,
      vipps_payout: false,
      provisional: false,
      partner_hint: null,
    },
  ] as BankRowSummary[],
  charges: [
    {
      id: 'charge1',
      source: 'vipps',
      date: '2026-09-02',
      amount: 1600,
      counterpart: 'Kari Testdotter',
      description: 'Slipping',
      reference: '3000100',
      suggestion_count: 0,
    },
  ] as VippsCharge[],
  chargeSuggestions: {
    charge1: {
      invoices: [],
      accounts: [
        {
          code: '3300',
          name: 'Salgsinntekter',
          account_type: 'revenue',
          score: 60,
          confidence_label: 'sannsynleg',
          why_tooltip: 'Slipping stor båt',
          reason: 'price_item',
        },
      ],
    },
  } as Record<string, BankRowSuggestions>,
}))

vi.mock('lucide-vue-next', () => ({
  ArrowDownLeft: icon,
  ArrowUpRight: icon,
  Sparkles: icon,
  Search: icon,
  FileText: icon,
  Ban: icon,
  Undo2: icon,
  CheckSquare: icon,
  X: icon,
  ChevronDown: icon,
  Check: icon,
}))

vi.mock('@/composables/useBankReconcile', async () => {
  const actual = await vi.importActual<typeof import('@/composables/useBankReconcile')>(
    '@/composables/useBankReconcile',
  )
  const mutation = () => ({ mutateAsync: vi.fn(), isPending: ref(false) })
  return {
    ...actual,
    fetchBankRowPartners: vi.fn(async () => []),
    useBankUnmatchedRows: () => ({ data: ref(rows), isLoading: ref(false) }),
    useBankRowSuggestions: () => ({ data: ref(undefined) }),
    useVippsChargeSuggestions: (id: Ref<string | null>) => ({
      data: computed(() => (id.value ? chargeSuggestions[id.value] : undefined)),
    }),
    useBankUnmatchedCountsByYear: () => ({ data: ref({}) }),
    useMissingVippsSettlements: () => ({ data: ref([]) }),
    useReconcileMutations: () => ({
      assignInvoice: mutation(),
      assignAccount: mutation(),
      assignAccountGroup: mutation(),
      dismiss: mutation(),
      unassign: mutation(),
      invalidate: vi.fn(),
    }),
    useAssignMultiInvoiceMutation: mutation,
    useVippsPerChargeEnabled: () => ({ data: ref(true) }),
    useUnreconciledVippsCharges: () => ({ data: ref(charges) }),
    useVippsChargeMutations: () => ({
      assignInvoice: mutation(),
      bilag: mutation(),
      dismiss: mutation(),
      clearPayout: mutation(),
    }),
  }
})

vi.mock('@/composables/useAccounting', () => ({
  useFiscalPeriods: () => ({ data: ref([]) }),
  useAccountsList: () => ({
    data: ref([
      {
        code: '3300',
        name: 'Salgsinntekter',
        account_type: 'revenue',
        is_active: true,
        price_items: [
          { id: 'p1', name: 'Slipping liten båt', category: 'service' },
          { id: 'p2', name: 'Slipping stor båt', category: 'service' },
          { id: 'p3', name: 'Spyling', category: 'service' },
          { id: 'p4', name: 'Kranløft', category: 'service' },
          { id: 'p5', name: 'Mastekran', category: 'service' },
        ],
        price_categories: ['service'],
      },
      {
        code: '3200',
        name: 'Gjestehavninntekter',
        account_type: 'revenue',
        is_active: true,
        price_items: [{ id: 'p6', name: 'Gjesteplass', category: 'guest' }],
        price_categories: ['guest'],
      },
      { code: '3900', name: 'Andre inntekter', account_type: 'revenue', is_active: true, price_items: [], price_categories: ['other'] },
    ]),
  }),
}))

function mountTab() {
  return mountWithPlugins(BankReconcileTab, { global: { stubs: { teleport: true } } })
}

async function openBankPicker() {
  const wrapper = mountTab()
  await wrapper.findAll('input[type="checkbox"]')[0].setValue(true)
  const lagBilag = wrapper.findAll('button').filter((b) => b.text().includes('admin.bankReconcile.lagBilag'))
  await lagBilag[lagBilag.length - 1].trigger('click')
  await flushPromises()
  return wrapper
}

function listedCodes(wrapper: ReturnType<typeof mountTab>): string[] {
  return wrapper
    .findAll('[data-testid^="assign-account-"]')
    .map((el) => el.attributes('data-testid')!.replace('assign-account-', ''))
}

describe('BankReconcileTab account search', () => {
  it('finds the account a price item books to by the item name, whatever the case or diacritics', async () => {
    const wrapper = await openBankPicker()
    const search = wrapper.get('input[type="search"][placeholder="admin.bankReconcile.searchAccountPlaceholder"]')

    await search.setValue('SLIPPING')
    expect(listedCodes(wrapper)).toEqual(['3300'])

    await search.setValue('stor bat')
    expect(listedCodes(wrapper)).toEqual(['3300'])

    await search.setValue('gjesteplass')
    expect(listedCodes(wrapper)).toEqual(['3200'])

    await search.setValue('slipping gjesteplass')
    expect(listedCodes(wrapper)).toEqual([])
  })

  it('finds an account by the label of a category mapped to it', async () => {
    const wrapper = await openBankPicker()
    const search = wrapper.get('input[type="search"][placeholder="admin.bankReconcile.searchAccountPlaceholder"]')
    // The test i18n returns keys, so the label is the category's key.
    await search.setValue('admin.pricing.categoryService')
    expect(listedCodes(wrapper)).toEqual(['3300'])
  })

  it('shows what books to each account, four item names at most', async () => {
    const wrapper = await openBankPicker()
    const row = wrapper.get('[data-testid="assign-account-3300"]').element.closest('li')!
    const hint = row.querySelector('[data-testid="account-hint"]')!.textContent!
    expect(hint).toContain('admin.pricing.categoryService · Slipping liten båt, Slipping stor båt, Spyling, Kranløft')
    expect(hint).toContain('admin.bankReconcile.accountMoreItems')
    expect(hint).not.toContain('Mastekran')
  })

  it('suggests the price item account for a Vipps charge', async () => {
    const wrapper = mountTab()
    const chargeLagBilag = wrapper.findAll('button').find((b) => b.text().includes('admin.bankReconcile.lagBilag'))!
    await chargeLagBilag.trigger('click')
    await flushPromises()

    const sug = wrapper.get('[data-testid="account-suggestion-3300"]')
    expect(sug.get('[data-testid="price-match-why"]').text()).toContain('admin.bankReconcile.priceMatchWhy')
    expect(sug.attributes('title')).toBe('Slipping stor båt')
  })
})
