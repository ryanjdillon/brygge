<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { ArrowDownLeft, ArrowUpRight, Sparkles, Search, FileText, Ban, Undo2, CheckSquare } from 'lucide-vue-next'
import Modal from '@/components/ui/Modal.vue'
import BankRowPartnerChoice from '@/components/admin/BankRowPartnerChoice.vue'
import PayerAliasToggle from '@/components/admin/PayerAliasToggle.vue'
import AccountOptionLabel from '@/components/admin/AccountOptionLabel.vue'
import StripePayoutPicker from '@/components/admin/StripePayoutPicker.vue'
import SuggestionsLoadError from '@/components/admin/SuggestionsLoadError.vue'
import Select from '@/components/ui/form/Select.vue'
import {
  useBankUnmatchedRows,
  useBankRowSuggestions,
  useVippsChargeSuggestions,
  useBankUnmatchedCountsByYear,
  useMissingVippsSettlements,
  useReconcileMutations,
  useAssignMultiInvoiceMutation,
  fetchPotentialInvoices,
  fetchBankRowPartners,
  fetchBankRowSuggestions,
  isHasPartnerError,
  isAliasConflictError,
  isImportedNoOpeningBalanceError,
  isInvoiceCreditedError,
  isInvoiceNotDebtError,
  payerAliasOffered,
  payerAliasFields,
  sharedPayerAlias,
  useVippsPerChargeEnabled,
  useUnreconciledVippsCharges,
  useVippsChargeMutations,
  fetchVippsChargePotentialInvoices,
  DISMISS_REASONS,
  BANK_ROW_DIRECTIONS,
  BANK_ROW_SOURCES,
  BANK_ROW_FLAGS,
  emptyBankRowFilter,
  type BankRowSummary,
  type BankRowFilter,
  type BankRowDirection,
  type BankRowSource,
  type BankRowFlag,
  type InvoiceSuggestion,
  type AccountSuggestion,
  type DismissReason,
  type VippsCharge,
  type PartnerSuggestion,
  type PayerAliasState,
  type PayerAliasFields,
} from '@/composables/useBankReconcile'
import { formatNOK as formatNOKBase, formatDate as fmtDate } from '@/lib/format'
import { useAccountsList } from '@/composables/useAccounting'
import { filterAccounts } from '@/lib/accountSearch'
import { priceCategoryKey } from '@/lib/priceCategories'
import { useConfirm, useConfirmStore } from '@/stores/confirm'
import { useToast } from '@/composables/useToast'
import { useFreshTotp } from '@/composables/useFreshTotp'
import { providerPayoutRefusal } from '@/composables/useStripePayouts'

const { t, locale } = useI18n()
const askConfirm = useConfirm()
const confirmStore = useConfirmStore()

const route = useRoute()
const router = useRouter()

// Filter state lives in the URL (?dir=&src=&flags=a,b) so it survives a
// reload and a tab switch. Unknown values are dropped.
function filterFromQuery(): BankRowFilter {
  const one = <T extends string>(v: unknown, allowed: T[]): T | null =>
    typeof v === 'string' && (allowed as string[]).includes(v) ? (v as T) : null
  const flagsRaw = typeof route.query.flags === 'string' ? route.query.flags.split(',') : []
  return {
    direction: one(route.query.dir, BANK_ROW_DIRECTIONS),
    source: one(route.query.src, BANK_ROW_SOURCES),
    flags: BANK_ROW_FLAGS.filter((f) => flagsRaw.includes(f)),
  }
}

const filter = ref<BankRowFilter>(filterFromQuery())
const filterActive = computed(
  () => !!filter.value.direction || !!filter.value.source || filter.value.flags.length > 0,
)

watch(
  filter,
  (f) => {
    const query = { ...route.query }
    delete query.dir
    delete query.src
    delete query.flags
    if (f.direction) query.dir = f.direction
    if (f.source) query.src = f.source
    if (f.flags.length) query.flags = f.flags.join(',')
    void router.replace({ query })
  },
  { deep: true },
)

// Direction and source are single-choice groups: clicking the active chip
// clears the group. Flags toggle independently. Groups narrow together.
function toggleDirection(d: BankRowDirection) {
  filter.value = { ...filter.value, direction: filter.value.direction === d ? null : d }
}
function toggleSource(s: BankRowSource) {
  filter.value = { ...filter.value, source: filter.value.source === s ? null : s }
}
function toggleFlag(fl: BankRowFlag) {
  const flags = filter.value.flags.includes(fl)
    ? filter.value.flags.filter((x) => x !== fl)
    : [...filter.value.flags, fl]
  filter.value = { ...filter.value, flags }
}
function clearFilters() {
  filter.value = emptyBankRowFilter()
}

// Unreconciled Vipps charges are incoming, never dismissed, and carry no
// duplicate flags, so they only belong in views a filter would let them
// through.
const showVippsCharges = computed(() => {
  const f = filter.value
  return (f.source === null || f.source === 'vipps')
    && (f.direction === null || f.direction === 'incoming')
    && f.flags.length === 0
})

const q = ref('')
const currentYear = new Date().getFullYear()

const { data: countsByYear } = useBankUnmatchedCountsByYear()

// 0 = "all years" sentinel for the Select component (which requires string|number values).
// Converted to null for API calls where 0 means no year filter.
const yearSelectValue = ref<number>(currentYear)
const year = computed(() => yearSelectValue.value === 0 ? null : yearSelectValue.value)

const yearOptions = computed(() => {
  const years = [0, ...Array.from({ length: 5 }, (_, i) => currentYear - i)]
  return years.map((y) => {
    const count = y === 0
      ? Object.values(countsByYear.value ?? {}).reduce((a, b) => a + b, 0)
      : (countsByYear.value?.[y] ?? 0)
    return {
      value: y,
      label: y === 0
        ? `${t('admin.bankReconcile.yearAll')}${count > 0 ? ` (${count})` : ''}`
        : `${y}${count > 0 ? ` (${count})` : ''}`,
    }
  })
})

const { data: rows, isLoading } = useBankUnmatchedRows(filter, q, year)

const duplicateRows = computed(() =>
  (rows.value ?? []).filter((r) => r.likely_duplicate_of_matched && !r.dismissed_at),
)
const mainRows = computed(() =>
  (rows.value ?? []).filter((r) => !r.likely_duplicate_of_matched || r.dismissed_at),
)

const focusedRowId = ref<string | null>(null)
const { data: suggestions } = useBankRowSuggestions(focusedRowId)

const { data: missingSettlements } = useMissingVippsSettlements()
const missingSettlementsTotal = computed(() =>
  (missingSettlements.value ?? []).reduce((sum, m) => sum + Number(m.amount), 0),
)

const { assignInvoice, assignAccount, assignAccountGroup, dismiss, unassign, invalidate: refetchBankRows } =
  useReconcileMutations()
const assignMultiInvoice = useAssignMultiInvoiceMutation()

// ── Per-charge Vipps model (BRY-227) ─────────────────────────
// When the club is on the per-charge model, individual Vipps charges appear as
// their own Tildel items and the batched payout bank rows render as quiet
// auto-clearing items instead of unassignable deposits.
const { data: perChargeEnabled } = useVippsPerChargeEnabled()
const vippsEnabled = computed(() => !!perChargeEnabled.value)
const { data: vippsCharges } = useUnreconciledVippsCharges(vippsEnabled)
const {
  assignInvoice: vAssignInvoice,
  bilag: vBilag,
  dismiss: vDismiss,
  clearPayout,
  invalidate: refetchVippsCharges,
} = useVippsChargeMutations()

// The modals below are shared between bank rows and Vipps charges; activeSource
// tells the submit handlers which mutation + candidate fetch to use.
const activeSource = ref<'bank' | 'vipps'>('bank')

// Remaining-charge counts from the last payout clear attempt, keyed by bank row
// id, so a not-yet-ready payout shows "N charges left" inline.
const payoutRemaining = ref<Record<string, number>>({})

async function doClearPayout(rowId: string) {
  const res = await clearPayout.mutateAsync({ rowId })
  if (res && !res.ready) {
    payoutRemaining.value = { ...payoutRemaining.value, [rowId]: res.unreconciled_charges }
  }
}

function openVippsPotential(charge: VippsCharge) {
  activeSource.value = 'vipps'
  potentialRowId.value = charge.id
  potentialQuery.value = ''
  potentialOpen.value = true
  void loadPotential()
}
function openVippsAccountPicker(charge: VippsCharge) {
  activeSource.value = 'vipps'
  accountPickerRowId.value = charge.id
  accountPickerKind.value = 'revenue'
  accountPickerSearch.value = ''
  bilagDescription.value = charge.description ?? ''
  pickerRows.value = []
  partnerChoice.value = null
  accountPickerOpen.value = true
}
function openVippsDismiss(charge: VippsCharge) {
  activeSource.value = 'vipps'
  dismissRowId.value = charge.id
  dismissReason.value = null
  dismissOpen.value = true
}

// ── Multi-select ─────────────────────────────────────────────
const selectedIds = ref<Set<string>>(new Set())
const selectedRows = computed(() =>
  (rows.value ?? []).filter((r) => selectedIds.value.has(r.id) && !r.dismissed_at),
)
const selectedTotal = computed(() => selectedRows.value.reduce((s, r) => s + r.amount, 0))

function toggleSelect(id: string) {
  const next = new Set(selectedIds.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  selectedIds.value = next
}
function clearSelection() {
  selectedIds.value = new Set()
}

function focusRow(id: string) {
  focusedRowId.value = focusedRowId.value === id ? null : id
}

function formatNOK(n: number): string {
  return formatNOKBase(n, { signed: true })
}
function formatDate(s: string): string {
  return fmtDate(s, locale.value)
}

const directionChips: { value: BankRowDirection; labelKey: string }[] = [
  { value: 'incoming', labelKey: 'admin.bankReconcile.filterIncoming' },
  { value: 'outgoing', labelKey: 'admin.bankReconcile.filterOutgoing' },
  { value: 'dismissed', labelKey: 'admin.bankReconcile.filterDismissed' },
]

const sourceChips: { value: BankRowSource; labelKey: string }[] = [
  { value: 'bank', labelKey: 'admin.bankReconcile.filterBank' },
  { value: 'vipps', labelKey: 'admin.bankReconcile.filterVipps' },
]

const flagChips: { value: BankRowFlag; labelKey: string }[] = [
  { value: 'duplicate', labelKey: 'admin.bankReconcile.filterDuplicate' },
  { value: 'double_payment', labelKey: 'admin.bankReconcile.filterDoublePayment' },
]

function chipClass(active: boolean): string {
  return active ? 'bg-brand-600 text-white' : 'bg-gray-100 text-gray-700 hover:bg-gray-200'
}

const confidenceClass: Record<string, string> = {
  sterk: 'border-emerald-300 bg-emerald-50',
  sannsynleg: 'border-amber-300 bg-amber-50',
  svak: 'border-gray-300 bg-gray-50',
  potensiell: 'border-brand-200 bg-brand-50',
}

const { data: accounts } = useAccountsList()
const expenseAccounts = computed(() => (accounts.value ?? []).filter((a) => a.account_type === 'expense' && a.is_active))
const revenueAccounts = computed(() => (accounts.value ?? []).filter((a) => a.account_type === 'revenue' && a.is_active))

// ── Potential invoices modal ─────────────────────────────────
const potentialOpen = ref(false)
const potentialRowId = ref<string | null>(null)
const potentialQuery = ref('')
const potentialItems = ref<InvoiceSuggestion[]>([])
const potentialLoading = ref(false)
const potentialError = ref(false)

async function openPotential(row: BankRowSummary) {
  activeSource.value = 'bank'
  potentialRowId.value = row.id
  potentialQuery.value = ''
  potentialOpen.value = true
  await loadPotential()
}
async function loadPotential() {
  if (!potentialRowId.value) return
  potentialLoading.value = true
  potentialError.value = false
  try {
    potentialItems.value = activeSource.value === 'vipps'
      ? await fetchVippsChargePotentialInvoices(potentialRowId.value, potentialQuery.value)
      : await fetchPotentialInvoices(potentialRowId.value, potentialQuery.value)
  } catch {
    // An empty list would read as "no matching faktura"; say the load failed.
    potentialItems.value = []
    potentialError.value = true
  } finally {
    potentialLoading.value = false
  }
}

// ── Remember the payer (BRY-383) ─────────────────────────────
// One "Hugs betalaren" box per row, above its faktura cards and in the
// search picker: ticked by default, unticked when the name already belongs
// to another member. Clicking a faktura sends the box's state; a faktura of
// the member the payer already resolves to never remembers.
const potentialStateRowId = computed(() =>
  potentialOpen.value && activeSource.value === 'bank' ? potentialRowId.value : null,
)
const { data: potentialSuggestions } = useBankRowSuggestions(potentialStateRowId)
const rememberChoice = ref<Record<string, boolean>>({})
function rememberFor(rowId: string, st: PayerAliasState | undefined): boolean {
  return rememberChoice.value[rowId] ?? !st?.taken
}
function setRemember(rowId: string, value: boolean) {
  rememberChoice.value = { ...rememberChoice.value, [rowId]: value }
}
const panelAlias = computed(() => suggestions.value?.payer_alias)
const panelAliasOffered = computed(() =>
  payerAliasOffered(panelAlias.value, suggestions.value?.invoices.slice(0, 3) ?? []),
)
const potentialAlias = computed(() => potentialSuggestions.value?.payer_alias)
const potentialAliasOffered = computed(() => payerAliasOffered(potentialAlias.value, potentialItems.value))

// Books with the alias fields; when the server refuses the alias because the
// name became another member's since the box was shown, asks whether to take
// it over or book without remembering. Resolves false when cancelled.
async function bookRemembering(alias: PayerAliasFields, book: (a: PayerAliasFields) => Promise<unknown>): Promise<boolean> {
  try {
    await book(alias)
    return true
  } catch (e) {
    if (!alias.remember_payer || !isAliasConflictError(e)) throw e
  }
  const ok = await confirmStore.ask({
    title: t('admin.bankReconcile.payerAlias.conflictTitle'),
    body: t('admin.bankReconcile.payerAlias.conflictBody'),
    tone: 'warning',
    choice: {
      label: t('admin.bankReconcile.payerAlias.conflictChoice'),
      options: [
        { value: 'skip', label: t('admin.bankReconcile.payerAlias.conflictSkip') },
        { value: 'override', label: t('admin.bankReconcile.payerAlias.conflictOverride') },
      ],
      default: 'skip',
    },
    confirmLabel: t('admin.bankReconcile.assign'),
  })
  if (!ok) return false
  const override = confirmStore.choiceValue === 'override'
  await book({ remember_payer: override, confirm_alias_override: override })
  return true
}

// ── Account picker / Lag bilag modal ─────────────────────────
const accountPickerOpen = ref(false)
const accountPickerRowId = ref<string | null>(null)
const accountPickerKind = ref<'expense' | 'revenue'>('expense')
const accountPickerSearch = ref('')
const bilagDescription = ref('')

function openAccountPicker(row: BankRowSummary) {
  activeSource.value = 'bank'
  accountPickerRowId.value = row.id
  accountPickerKind.value = row.amount < 0 ? 'expense' : 'revenue'
  accountPickerSearch.value = ''
  bilagDescription.value = row.description ?? ''
  pickerRows.value = selectedRows.value.length > 1 ? [...selectedRows.value] : [row]
  accountPickerOpen.value = true
  void loadPickerPartners()
}
const filteredAccounts = computed(() => {
  const base = accountPickerKind.value === 'expense' ? expenseAccounts.value : revenueAccounts.value
  return matchAccountSearch(base)
})
// Search covers what books to an account as well as its code and name, so
// "slipping" finds the account slipping books to.
function categoryLabel(category: string): string {
  const key = priceCategoryKey(category)
  return key ? t(key) : category
}
function matchAccountSearch<T extends { code: string; name: string }>(base: T[]): T[] {
  return filterAccounts(base, accountPickerSearch.value, categoryLabel)
}

// ── Rows that belong together (BRY-380) ──────────────────────
// Lag bilag shows each row's likely partner rows. A strong partner must be
// either booked together (one bilag for the net) or explicitly declined;
// the server refuses the row on its own otherwise (409 has_partner).
const pickerRows = ref<BankRowSummary[]>([])
const pickerPartners = ref<Record<string, PartnerSuggestion[]>>({})
const partnersLoading = ref(false)
const partnerChoice = ref<string | null>(null)
const ackSeparate = ref(false)
const partnerNotice = ref('')
const MAX_GROUP_ROWS = 5

// The picker loads suggestions for the row it was opened on, never the
// expanded row's: a picker opened from the bulk toolbar or after expanding
// another row would otherwise offer that other row's accounts. Several rows
// picked at once have no single row to suggest for.
const pickerSuggestionRowId = computed(() =>
  accountPickerOpen.value && activeSource.value === 'bank' && pickerRows.value.length === 1
    ? accountPickerRowId.value
    : null,
)
const pickerChargeId = computed(() =>
  accountPickerOpen.value && activeSource.value === 'vipps' ? accountPickerRowId.value : null,
)
const { data: pickerSuggestions } = useBankRowSuggestions(pickerSuggestionRowId)
const { data: chargeSuggestions } = useVippsChargeSuggestions(pickerChargeId)
const accountPickerSuggestions = computed<AccountSuggestion[]>(() => {
  if (pickerSuggestionRowId.value) return pickerSuggestions.value?.accounts ?? []
  if (pickerChargeId.value) return chargeSuggestions.value?.accounts ?? []
  return []
})

// Each load supersedes the one before: a late answer for an earlier row
// must not overwrite the partners or the loading state of the current one.
let partnerRequest = 0
async function loadPickerPartners() {
  const request = ++partnerRequest
  partnerChoice.value = null
  ackSeparate.value = false
  partnerNotice.value = ''
  pickerPartners.value = {}
  partnersLoading.value = true
  try {
    const entries = await Promise.all(
      pickerRows.value.map(async (r) => [r.id, await fetchBankRowPartners(r.id)] as const),
    )
    if (request === partnerRequest) pickerPartners.value = Object.fromEntries(entries)
  } finally {
    if (request === partnerRequest) partnersLoading.value = false
  }
}

const singlePick = computed(() => pickerRows.value.length === 1)
const singlePartners = computed(() =>
  singlePick.value ? (pickerPartners.value[pickerRows.value[0].id] ?? []) : [],
)
const pickedIds = computed(() => new Set(pickerRows.value.map((r) => r.id)))
const pairedWithinSelection = (r: BankRowSummary) =>
  (pickerPartners.value[r.id] ?? []).some((p) => pickedIds.value.has(p.id))
// Every selected row has a suggested partner among the selection: book
// them as one group.
const selectionIsGroup = computed(
  () => !singlePick.value && pickerRows.value.length > 0 && pickerRows.value.every(pairedWithinSelection),
)
const selectionPartlyPaired = computed(
  () => !singlePick.value && !selectionIsGroup.value && pickerRows.value.some(pairedWithinSelection),
)
// Selected rows with a strong partner left out of the selection.
const strongOutside = computed(() =>
  singlePick.value
    ? []
    : pickerRows.value.flatMap((r) =>
        (pickerPartners.value[r.id] ?? [])
          .filter((p) => p.strength === 'strong' && !pickedIds.value.has(p.id))
          .map((p) => ({ row: r, partner: p })),
      ),
)

const groupRowIds = computed<string[] | null>(() => {
  if (singlePick.value) {
    const c = partnerChoice.value
    return c && c !== 'alone' ? [pickerRows.value[0].id, c] : null
  }
  return selectionIsGroup.value ? pickerRows.value.map((r) => r.id) : null
})
const groupNet = computed(() => {
  if (!groupRowIds.value) return 0
  if (singlePick.value) return singlePartners.value.find((p) => p.id === partnerChoice.value)?.net ?? 0
  return Math.round(pickerRows.value.reduce((sum, r) => sum + r.amount * 100, 0)) / 100
})

const assignBlockedReason = computed<string | null>(() => {
  if (activeSource.value !== 'bank') return null
  if (partnersLoading.value) return t('common.loading')
  if (singlePick.value) {
    const strong = singlePartners.value.some((p) => p.strength === 'strong')
    return strong && partnerChoice.value === null ? t('admin.bankReconcile.partners.choiceRequired') : null
  }
  if (selectionPartlyPaired.value) return t('admin.bankReconcile.partners.bulkPartlyPaired')
  if (selectionIsGroup.value && pickerRows.value.length > MAX_GROUP_ROWS) {
    return t('admin.bankReconcile.partners.bulkTooMany', { max: MAX_GROUP_ROWS })
  }
  if (strongOutside.value.length && !ackSeparate.value) return t('admin.bankReconcile.partners.bulkAckRequired')
  return null
})

// Booked together, the net may land on any non-bank account: a buy-back
// net of a returned overpayment belongs on a balance account, not revenue.
const RECEIVABLES_CODE = '1500'
const ACCOUNT_TYPE_ORDER: Record<'in' | 'out', string[]> = {
  in: ['revenue', 'liability', 'equity', 'asset', 'expense'],
  out: ['expense', 'liability', 'equity', 'asset', 'revenue'],
}
const groupAccountSections = computed(() => {
  const active = (accounts.value ?? []).filter((a) => a.is_active && !a.code.startsWith('19') && a.code !== RECEIVABLES_CODE)
  const order = ACCOUNT_TYPE_ORDER[groupNet.value < 0 ? 'out' : 'in']
  return order
    .map((type) => ({ type, accounts: matchAccountSearch(active.filter((a) => a.account_type === type)) }))
    .filter((sec) => sec.accounts.length > 0)
})

// An undismissed Stripe payout deposit is booked only by a payout clearing:
// the sweep's, or a person's through the picker.
function isStripePayout(row: BankRowSummary): boolean {
  return row.provider_payout === 'stripe' && !row.dismissed_at
}

// ── Clear a Stripe payout by hand ────────────────────────────
const { ensureFreshTotp } = useFreshTotp()
const stripePickerOpen = ref(false)
const stripePickerRow = ref<BankRowSummary | null>(null)

// The clearing posts to the ledger, so the step-up comes before the picker.
async function openStripePicker(row: BankRowSummary) {
  if (!(await ensureFreshTotp())) return
  stripePickerRow.value = row
  stripePickerOpen.value = true
}

// ── Dismiss reason modal ─────────────────────────────────────
const dismissOpen = ref(false)
const dismissRowId = ref<string | null>(null)
const dismissReason = ref<DismissReason | null>(null)

function openDismiss(row: BankRowSummary) {
  activeSource.value = 'bank'
  dismissRowId.value = row.id
  if (isStripePayout(row)) dismissReason.value = 'internal_transfer'
  else if (row.likely_duplicate_of_matched) dismissReason.value = 'duplicate'
  else if (row.possible_double_payment) dismissReason.value = 'double_payment'
  else dismissReason.value = null
  dismissOpen.value = true
}

async function submitDismiss() {
  if (!dismissRowId.value || !dismissReason.value) return
  if (activeSource.value === 'vipps') {
    await vDismiss.mutateAsync({ rowId: dismissRowId.value, reason: dismissReason.value })
  } else {
    await dismiss.mutateAsync({ rowId: dismissRowId.value, reason: dismissReason.value })
  }
  dismissOpen.value = false
}

// Runs an assign and tells the treasurer when the server refused it because
// the faktura is imported and its receivable isn't in the opening balance yet
// (BRY-386), because the faktura is credited (a stale list could still offer
// it, BRY-508), or because the row is a provider's payout deposit (only its
// clearing books it; a stale page could still offer the assign): nothing was
// booked and the row stays here. Resolves false then.
const toast = useToast()
async function refusedAsImported(book: () => Promise<boolean>): Promise<boolean> {
  try {
    return await book()
  } catch (e) {
    if (refusedAsProviderPayout(e)) return false
    const notAssignable = isInvoiceCreditedError(e)
      ? 'admin.bankReconcile.invoiceCreditedRefused'
      : isInvoiceNotDebtError(e)
        ? 'admin.bankReconcile.invoiceNotDebtRefused'
        : null
    if (notAssignable) {
      toast.error(t(notAssignable), 10000)
      // The faktura was offered from a stale list or panel: refresh the
      // suggestions, the counts and the search results it came from.
      refetchBankRows()
      refetchVippsCharges()
      if (potentialOpen.value) void loadPotential()
      if (multiInvoiceOpen.value) void loadMultiInvoice()
      return false
    }
    if (!isImportedNoOpeningBalanceError(e)) throw e
    toast.error(t('admin.accounting.importedNoOpeningBalance'), 10000)
    return false
  }
}
function refusedAsProviderPayout(e: unknown): boolean {
  const provider = providerPayoutRefusal(e)
  if (!provider) return false
  toast.error(t('admin.bankReconcile.providerPayoutRefused', { provider: provider === 'vipps' ? 'Vipps' : 'Stripe' }), 10000)
  refetchBankRows()
  return true
}

// ── Multi-invoice modal ──────────────────────────────────────
const multiInvoiceOpen = ref(false)
const multiInvoiceQuery = ref('')
const multiInvoiceItems = ref<InvoiceSuggestion[]>([])
const multiInvoiceLoading = ref(false)
const multiInvoiceError = ref(false)
// The box shows only when every selected row has the same payer.
const multiAlias = ref<PayerAliasState | null>(null)
const multiRemember = ref(true)
const multiAliasOffered = computed(() => payerAliasOffered(multiAlias.value, multiInvoiceItems.value))

async function openMultiInvoice() {
  multiInvoiceQuery.value = ''
  multiAlias.value = null
  multiInvoiceOpen.value = true
  await Promise.all([loadMultiInvoice(), loadMultiAlias()])
}
async function loadMultiAlias() {
  const states = await Promise.all(
    selectedRows.value.map(async (r) => (await fetchBankRowSuggestions(r.id)).payer_alias),
  )
  multiAlias.value = sharedPayerAlias(states)
  multiRemember.value = !multiAlias.value?.taken
}
async function loadMultiInvoice() {
  if (!selectedRows.value.length) return
  multiInvoiceLoading.value = true
  multiInvoiceError.value = false
  try {
    multiInvoiceItems.value = await fetchPotentialInvoices(selectedRows.value[0].id, multiInvoiceQuery.value, selectedTotal.value)
  } catch {
    multiInvoiceItems.value = []
    multiInvoiceError.value = true
  } finally {
    multiInvoiceLoading.value = false
  }
}
async function doAssignMultiInvoice(inv: InvoiceSuggestion) {
  const rowIds = [...selectedIds.value]
  const alias = payerAliasFields(multiAlias.value, multiAliasOffered.value, multiRemember.value, inv)
  const booked = await refusedAsImported(() =>
    bookRemembering(alias, (a) => assignMultiInvoice.mutateAsync({ rowIds, invoiceId: inv.invoice_id, alias: a })),
  )
  if (!booked) return
  multiInvoiceOpen.value = false
  clearSelection()
}

// ── Actions ──────────────────────────────────────────────────
async function doAssignInvoice(
  rowId: string,
  inv: InvoiceSuggestion,
  alias?: { state: PayerAliasState | undefined; offered: boolean },
) {
  const invoiceId = inv.invoice_id
  const booked = await refusedAsImported(async () => {
    if (activeSource.value === 'vipps') {
      await vAssignInvoice.mutateAsync({ rowId, invoiceId })
      return true
    }
    const fields = payerAliasFields(alias?.state, !!alias?.offered, rememberFor(rowId, alias?.state), inv)
    return bookRemembering(fields, (a) => assignInvoice.mutateAsync({ rowId, invoiceId, alias: a }))
  })
  if (!booked) return
  potentialOpen.value = false
}
async function doAssignAccount(rowId: string, accountCode: string, k: 'expense' | 'revenue') {
  const description = bilagDescription.value || undefined
  if (activeSource.value === 'vipps') {
    await vBilag.mutateAsync({ rowId, accountCode, description })
    accountPickerOpen.value = false
    return
  }
  if (assignBlockedReason.value) return
  if (groupRowIds.value) {
    try {
      await assignAccountGroup.mutateAsync({ rowIds: groupRowIds.value, accountCode, description })
    } catch (e) {
      if (!refusedAsProviderPayout(e)) throw e
      return
    }
    accountPickerOpen.value = false
    clearSelection()
    return
  }
  // Booking alone is confirmed only once the partners have been shown:
  // the explicit "alone" choice, or the bulk acknowledgement.
  const confirmAlone = singlePick.value ? partnerChoice.value === 'alone' : ackSeparate.value
  const targetIds = pickerRows.value.map((r) => r.id)
  let booked = 0
  try {
    for (const id of targetIds) {
      await assignAccount.mutateAsync({ rowId: id, accountCode, kind: k, description, confirmAlone })
      booked++
    }
  } catch (e) {
    if (refusedAsProviderPayout(e)) return
    if (!isHasPartnerError(e)) throw e
    // The list was stale: a partner appeared since it loaded. Show the
    // choice again for what is left.
    refetchBankRows()
    pickerRows.value = pickerRows.value.slice(booked)
    await loadPickerPartners()
    partnerNotice.value = t('admin.bankReconcile.partners.stale', { booked }, booked)
    return
  }
  accountPickerOpen.value = false
  clearSelection()
}
async function doUnassign(rowId: string) {
  const ok = await askConfirm({
    title: t('admin.bankReconcile.unassignTitle'),
    body: t('admin.bankReconcile.unassignConfirm'),
    confirmLabel: t('admin.bankReconcile.unassignAction'),
    tone: 'danger',
  })
  if (!ok) return
  await unassign.mutateAsync({ rowId })
}
</script>

<template>
  <div>
    <!-- Search + filter chips. Each group (direction | source | flags)
         narrows the list; groups combine. -->
    <div class="flex flex-wrap items-center gap-2" data-testid="reconcile-filters">
      <div class="flex items-center gap-2">
        <Search class="h-4 w-4 text-gray-400" />
        <input
          v-model="q"
          type="search"
          :placeholder="t('admin.bankReconcile.searchPlaceholder')"
          class="w-56 rounded-md border border-gray-300 px-3 py-1 text-sm"
        />
      </div>
      <button
        type="button"
        class="rounded-full px-3 py-1 text-xs font-semibold"
        :class="chipClass(!filterActive)"
        :aria-pressed="!filterActive"
        data-testid="filter-all"
        @click="clearFilters"
      >
        {{ t('admin.bankReconcile.filterAll') }}
      </button>
      <span class="h-4 w-px bg-gray-300" aria-hidden="true" />
      <div role="group" :aria-label="t('admin.bankReconcile.filterGroupDirection')" class="flex flex-wrap gap-2">
        <button
          v-for="chip in directionChips"
          :key="chip.value"
          type="button"
          class="rounded-full px-3 py-1 text-xs font-semibold"
          :class="chipClass(filter.direction === chip.value)"
          :aria-pressed="filter.direction === chip.value"
          :data-testid="`filter-${chip.value}`"
          @click="toggleDirection(chip.value)"
        >
          {{ t(chip.labelKey) }}
        </button>
      </div>
      <span class="h-4 w-px bg-gray-300" aria-hidden="true" />
      <div role="group" :aria-label="t('admin.bankReconcile.filterGroupSource')" class="flex flex-wrap gap-2">
        <button
          v-for="chip in sourceChips"
          :key="chip.value"
          type="button"
          class="rounded-full px-3 py-1 text-xs font-semibold"
          :class="chipClass(filter.source === chip.value)"
          :aria-pressed="filter.source === chip.value"
          :data-testid="`filter-${chip.value}`"
          @click="toggleSource(chip.value)"
        >
          {{ t(chip.labelKey) }}
        </button>
      </div>
      <span class="h-4 w-px bg-gray-300" aria-hidden="true" />
      <div role="group" :aria-label="t('admin.bankReconcile.filterGroupFlags')" class="flex flex-wrap gap-2">
        <button
          v-for="chip in flagChips"
          :key="chip.value"
          type="button"
          class="rounded-full px-3 py-1 text-xs font-semibold"
          :class="chipClass(filter.flags.includes(chip.value))"
          :aria-pressed="filter.flags.includes(chip.value)"
          :data-testid="`filter-${chip.value}`"
          @click="toggleFlag(chip.value)"
        >
          {{ t(chip.labelKey) }}
        </button>
      </div>
      <div class="ml-auto flex items-center gap-2">
        <label class="text-xs text-gray-600">{{ t('admin.bankReconcile.year') }}</label>
        <Select v-model="yearSelectValue" :options="yearOptions" width="content" />
      </div>
    </div>

    <!-- Vipps payouts whose settlement export isn't imported: they can't be
         split into member payments, so a faktura paid inside one still looks
         unpaid. Warn before reconciling / sending purringer. -->
    <div
      v-if="missingSettlements?.length"
      class="mt-4 rounded-md border border-orange-300 bg-orange-50 p-3"
      data-testid="missing-vipps-settlements"
    >
      <p class="mb-1 text-xs font-semibold text-orange-900">
        ⚠
        {{
          t('admin.bankReconcile.missingSettlementsHeader', {
            count: missingSettlements.length,
            total: formatNOK(missingSettlementsTotal),
          })
        }}
      </p>
      <p class="mb-2 text-xs text-orange-700">{{ t('admin.bankReconcile.missingSettlementsHint') }}</p>
      <ul class="space-y-1 text-xs text-orange-900">
        <li v-for="m in missingSettlements" :key="m.bank_row_id" class="flex flex-wrap gap-x-3">
          <span class="font-mono">Utb. {{ m.settlement_number }} · Vippsnr {{ m.msn }}</span>
          <span>{{ fmtDate(m.date) }}</span>
          <span class="font-semibold">{{ formatNOK(m.amount) }}</span>
        </li>
      </ul>
    </div>

    <!-- Individual Vipps charges (per-charge model): one line per payer,
         reconciled independently of the batched payout below. -->
    <div v-if="vippsEnabled && vippsCharges?.length && showVippsCharges" class="mt-4">
      <p class="mb-2 text-xs font-semibold uppercase text-gray-500">
        {{ t('admin.bankReconcile.vippsChargesHeader', { count: vippsCharges.length }) }}
      </p>
      <ul class="space-y-3">
        <li
          v-for="c in vippsCharges"
          :key="c.id"
          class="rounded-md border border-violet-200 bg-white p-4"
        >
          <div class="flex items-start justify-between gap-4">
            <div class="flex items-start gap-3">
              <ArrowDownLeft class="mt-0.5 h-5 w-5 shrink-0 text-emerald-600" />
              <div>
                <p class="text-sm">
                  <span class="rounded bg-violet-100 px-1.5 py-0.5 text-[10px] font-semibold text-violet-700">Vipps</span>
                  <span class="ml-2 font-semibold tabular-nums text-emerald-700">{{ formatNOK(c.amount) }}</span>
                  <span class="ml-2 text-gray-500">{{ formatDate(c.date) }}</span>
                  <span v-if="c.reference" class="ml-2 rounded bg-gray-100 px-1.5 py-0.5 font-mono text-[10px] text-gray-600">Utb. {{ c.reference }}</span>
                </p>
                <p v-if="c.counterpart" class="mt-0.5 text-sm font-medium text-gray-900">{{ c.counterpart }}</p>
                <p class="mt-0.5 truncate text-xs text-gray-500" :title="c.description">{{ c.description || '—' }}</p>
                <p v-if="c.suggestion_count > 0" class="mt-0.5 inline-flex items-center gap-1 text-[10px] text-brand-700">
                  <Sparkles class="h-3 w-3" />
                  {{ t('admin.bankReconcile.vippsChargeSuggestions', { count: c.suggestion_count > 9 ? '9+' : c.suggestion_count }) }}
                </p>
              </div>
            </div>
            <div class="flex shrink-0 flex-wrap items-center justify-end gap-2">
              <button
                type="button"
                class="inline-flex items-center gap-1 rounded-md border border-gray-300 bg-white px-3 py-1 text-xs font-semibold text-gray-700 hover:bg-gray-50"
                @click="openVippsPotential(c)"
              >
                <Search class="h-3 w-3" />
                {{ t('admin.bankReconcile.searchInvoice') }}
              </button>
              <button
                type="button"
                class="inline-flex items-center gap-1 rounded-md border border-gray-300 bg-white px-3 py-1 text-xs font-semibold text-gray-700 hover:bg-gray-50"
                @click="openVippsAccountPicker(c)"
              >
                <FileText class="h-3 w-3" />
                {{ t('admin.bankReconcile.lagBilag') }}
              </button>
              <button
                type="button"
                class="inline-flex items-center gap-1 rounded-md border border-amber-300 bg-white px-3 py-1 text-xs font-semibold text-amber-700 hover:bg-amber-50"
                @click="openVippsDismiss(c)"
              >
                <Ban class="h-3 w-3" />
                {{ t('admin.bankReconcile.dismiss') }}
              </button>
            </div>
          </div>
        </li>
      </ul>
    </div>

    <p v-if="isLoading" class="mt-6 text-sm text-gray-500">{{ t('common.loading') }}…</p>
    <p v-else-if="!rows?.length" class="mt-6 rounded-md border border-dashed border-gray-300 px-4 py-6 text-center text-sm text-gray-500">
      {{ t('admin.bankReconcile.empty') }}
    </p>

    <template v-else>
      <!-- Confirmed Arkivref duplicates — grouped at top for quick dismissal -->
      <div v-if="duplicateRows.length" class="mt-4 rounded-md border border-amber-200 bg-amber-50 p-3">
        <p class="mb-1 text-xs font-semibold text-amber-900">
          ⚠ {{ t('admin.bankReconcile.confirmedDuplicatesHeader', { count: duplicateRows.length }) }}
        </p>
        <p class="mb-3 text-xs text-amber-700">{{ t('admin.bankReconcile.confirmedDuplicatesHint') }}</p>
        <ul class="space-y-2">
          <li
            v-for="row in duplicateRows"
            :key="row.id"
            class="flex items-center justify-between gap-4 rounded-md border border-amber-200 bg-white px-4 py-3"
          >
            <div class="flex items-start gap-3">
              <ArrowDownLeft class="mt-0.5 h-5 w-5 shrink-0 text-emerald-600" />
              <div>
                <p class="text-sm">
                  <span class="font-semibold tabular-nums text-emerald-700">{{ formatNOK(row.amount) }}</span>
                  <span class="ml-2 text-gray-500">{{ formatDate(row.row_date) }}</span>
                  <span class="ml-2 rounded bg-gray-100 px-1.5 py-0.5 font-mono text-[10px] text-gray-600">{{ row.bank_account_code }}</span>
                </p>
                <p v-if="row.counterpart" class="mt-0.5 text-sm font-medium text-gray-900">{{ row.counterpart }}</p>
                <p class="mt-0.5 truncate text-xs text-gray-500" :title="row.description">{{ row.description || '—' }}</p>
                <p v-if="row.kid_number || row.reference" class="mt-0.5 flex flex-wrap gap-1 text-[10px] text-gray-500">
                  <span v-if="row.kid_number" class="rounded bg-gray-100 px-1.5 py-0.5 font-mono">KID {{ row.kid_number }}</span>
                  <span v-if="row.reference" class="rounded bg-gray-100 px-1.5 py-0.5 font-mono">Ref {{ row.reference }}</span>
                </p>
              </div>
            </div>
            <div class="flex shrink-0 items-center gap-2">
              <button
                type="button"
                class="inline-flex items-center gap-1 rounded-md border border-gray-300 bg-white px-3 py-1 text-xs font-semibold text-gray-700 hover:bg-gray-50"
                :title="t('admin.bankReconcile.notADuplicateHint')"
                @click="openPotential(row)"
              >
                <Search class="h-3 w-3" />
                {{ t('admin.bankReconcile.searchInvoice') }}
              </button>
              <button
                type="button"
                class="inline-flex items-center gap-1 rounded-md border border-amber-300 bg-white px-3 py-1 text-xs font-semibold text-amber-700 hover:bg-amber-50"
                @click="openDismiss(row)"
              >
                <Ban class="h-3 w-3" />
                {{ t('admin.bankReconcile.dismiss') }}
              </button>
            </div>
          </li>
        </ul>
      </div>

      <!-- Main unmatched rows -->
      <ul v-if="mainRows.length" class="mt-4 space-y-3">
        <li
          v-for="row in mainRows"
          :key="row.id"
          class="rounded-md border bg-white p-4 transition-colors"
          :class="selectedIds.has(row.id) ? 'border-brand-400 ring-1 ring-brand-300' : 'border-gray-200'"
        >
          <!-- Bank row info -->
          <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between sm:gap-4">
            <div class="flex items-start gap-3">
              <input
                v-if="!row.dismissed_at && !isStripePayout(row)"
                type="checkbox"
                :checked="selectedIds.has(row.id)"
                class="mt-1 h-4 w-4 shrink-0 cursor-pointer rounded border-gray-300 text-brand-600"
                @change="toggleSelect(row.id)"
              />
              <component
                v-else-if="row.dismissed_at"
                :is="row.amount >= 0 ? ArrowDownLeft : ArrowUpRight"
                :class="row.amount >= 0 ? 'text-emerald-600' : 'text-gray-500'"
                class="mt-0.5 h-5 w-5 shrink-0"
              />
              <component
                v-if="!row.dismissed_at"
                :is="row.amount >= 0 ? ArrowDownLeft : ArrowUpRight"
                :class="row.amount >= 0 ? 'text-emerald-600' : 'text-gray-500'"
                class="mt-0.5 h-5 w-5 shrink-0"
              />
              <div>
                <p class="text-sm">
                  <span class="font-semibold tabular-nums" :class="row.amount >= 0 ? 'text-emerald-700' : 'text-gray-700'">
                    {{ formatNOK(row.amount) }}
                  </span>
                  <span class="ml-2 text-gray-500">{{ formatDate(row.row_date) }}</span>
                  <span class="ml-2 rounded bg-gray-100 px-1.5 py-0.5 font-mono text-[10px] text-gray-600">{{ row.bank_account_code }}</span>
                </p>
                <p v-if="row.counterpart" class="mt-0.5 text-sm font-medium text-gray-900">{{ row.counterpart }}</p>
                <p class="mt-0.5 truncate text-xs text-gray-500" :title="row.description">{{ row.description || '—' }}</p>
                <p v-if="row.kid_number || row.reference" class="mt-0.5 flex flex-wrap gap-1 text-[10px] text-gray-500">
                  <span v-if="row.kid_number" class="rounded bg-gray-100 px-1.5 py-0.5 font-mono">KID {{ row.kid_number }}</span>
                  <span v-if="row.reference" class="rounded bg-gray-100 px-1.5 py-0.5 font-mono">Ref {{ row.reference }}</span>
                </p>
                <p v-if="row.dismissed_at" class="mt-1 text-xs text-amber-700">
                  {{ t('admin.bankReconcile.dismissedAs', { reason: t('admin.bankReconcile.reasons.' + (row.dismissed_reason ?? '')) }) }}
                </p>
                <p v-if="row.possible_double_payment && !row.dismissed_at" class="mt-1 inline-flex items-center gap-1 rounded-full bg-red-100 px-2 py-0.5 text-[10px] font-semibold text-red-800">
                  ⚠ {{ t('admin.bankReconcile.possibleDoublePayment') }}
                </p>
                <p
                  v-if="row.provisional && !row.dismissed_at"
                  class="mt-1 inline-flex items-center gap-1 rounded-full bg-amber-100 px-2 py-0.5 text-[10px] font-semibold text-amber-800"
                  :title="t('admin.bankReconcile.provisionalHint')"
                  data-testid="bank-row-provisional"
                >
                  {{ t('admin.bankReconcile.provisionalBadge') }}
                </p>
                <p
                  v-if="row.partner_hint && !row.dismissed_at"
                  class="mt-1 inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] font-semibold"
                  :class="row.partner_hint === 'strong' ? 'bg-indigo-100 text-indigo-800' : 'bg-gray-100 text-gray-700'"
                  :title="t('admin.bankReconcile.partners.badgeHint')"
                  data-testid="bank-row-partner-hint"
                >
                  {{ row.partner_hint === 'strong' ? t('admin.bankReconcile.partners.badgeStrong') : t('admin.bankReconcile.partners.badgeWeak') }}
                </p>
              </div>
            </div>
            <!-- Batched Vipps payout under the per-charge model: quiet
                 auto-clearing item, not an assignable deposit. -->
            <div v-if="vippsEnabled && row.vipps_payout" class="flex flex-col items-start gap-1 text-left sm:items-end sm:text-right">
              <span class="rounded-full bg-violet-100 px-2 py-0.5 text-[10px] font-semibold text-violet-700">
                {{ t('admin.bankReconcile.vippsAutoClearing') }}
              </span>
              <button
                type="button"
                class="inline-flex items-center gap-1 rounded-md border border-violet-300 bg-white px-2.5 py-1 text-xs font-semibold text-violet-700 hover:bg-violet-50 disabled:opacity-50"
                :disabled="clearPayout.isPending.value"
                @click="doClearPayout(row.id)"
              >
                {{ t('admin.bankReconcile.vippsClearPayout') }}
              </button>
              <span v-if="payoutRemaining[row.id]" class="text-[10px] text-amber-700">
                {{ t('admin.bankReconcile.vippsChargesRemaining', { count: payoutRemaining[row.id] }) }}
              </span>
            </div>
            <!-- Stripe payout deposit: booked only by a payout clearing, so no
                 faktura or account suggestions (a faktura assign would credit
                 1500 a second time and the payout would never clear). One no
                 sweep will clear is cleared here against the payout a person
                 picks; one that is not a payout at all is dismissed. -->
            <div
              v-else-if="isStripePayout(row)"
              class="flex w-full flex-col items-start gap-1 text-left sm:w-auto sm:max-w-xs sm:items-end sm:text-right"
              data-testid="bank-row-stripe-payout"
            >
              <span class="rounded-full bg-violet-100 px-2 py-0.5 text-[10px] font-semibold text-violet-700">
                {{ t('admin.bankReconcile.stripeAutoClearing') }}
              </span>
              <span v-if="row.provider_payout_reason" class="text-[10px] text-amber-700" data-testid="bank-row-stripe-payout-reason">
                {{ row.provider_payout_reason }}
              </span>
              <div
                v-if="row.provider_payout_by_hand"
                class="flex flex-col items-start gap-1 sm:items-end"
                data-testid="bank-row-stripe-payout-by-hand"
              >
                <span class="text-xs text-gray-600">{{ t('admin.bankReconcile.stripeByHandHint') }}</span>
                <div class="flex flex-wrap justify-start gap-2 sm:justify-end">
                  <button
                    type="button"
                    class="inline-flex min-h-11 items-center gap-1 rounded-md border border-violet-300 bg-white px-3 py-1 text-xs font-semibold text-violet-700 hover:bg-violet-50"
                    data-testid="bank-row-stripe-clear"
                    @click="openStripePicker(row)"
                  >
                    {{ t('admin.bankReconcile.stripeClearAction') }}
                  </button>
                  <button
                    type="button"
                    class="inline-flex min-h-11 items-center gap-1 rounded-md border border-amber-300 bg-white px-3 py-1 text-xs font-semibold text-amber-700 hover:bg-amber-50"
                    @click="openDismiss(row)"
                  >
                    <Ban class="h-3 w-3" />
                    {{ t('admin.bankReconcile.dismiss') }}
                  </button>
                </div>
              </div>
            </div>
            <button
              v-else
              type="button"
              class="text-xs hover:underline"
              :class="
                focusedRowId !== row.id && row.amount > 0 && row.suggestion_count === 0
                  ? 'text-gray-400'
                  : 'text-brand-700'
              "
              @click="focusRow(row.id)"
            >
              <template v-if="focusedRowId === row.id">{{ t('admin.bankReconcile.hideSuggestions') }}</template>
              <template v-else-if="row.amount > 0 && row.suggestion_count === 0">
                {{ t('admin.bankReconcile.noInvoiceSuggestions') }}
              </template>
              <template v-else>
                {{ t('admin.bankReconcile.showSuggestions')
                }}<span v-if="row.amount > 0"> ({{ row.suggestion_count > 9 ? '9+' : row.suggestion_count }})</span>
              </template>
            </button>
          </div>

          <!-- Suggestions, only when focused -->
          <div v-if="focusedRowId === row.id && suggestions" class="mt-3 space-y-2">
            <!-- Named row, but nothing matches the payer's name: the suggestions
                 below coincide on amount only. Warn so they aren't mistaken for
                 this payer's invoice. -->
            <p
              v-if="
                row.counterpart &&
                suggestions.invoices.length > 0 &&
                !suggestions.invoices.some((s) => s.score >= 50)
              "
              class="rounded-md bg-amber-50 px-3 py-2 text-xs text-amber-800"
            >
              {{ t('admin.bankReconcile.noNameMatchWarning', { name: row.counterpart }) }}
            </p>
            <PayerAliasToggle
              v-if="panelAliasOffered && panelAlias"
              :model-value="rememberFor(row.id, panelAlias)"
              :payer="panelAlias.payer"
              :taken="panelAlias.taken"
              @update:model-value="setRemember(row.id, $event)"
            />
            <div
              v-for="sug in suggestions.invoices.slice(0, 3)"
              :key="sug.invoice_id"
              class="flex items-center justify-between gap-3 rounded-md border px-3 py-2 text-sm"
              :class="confidenceClass[sug.confidence_label]"
            >
              <div class="min-w-0 flex-1">
                <p class="flex items-center gap-2">
                  <Sparkles class="h-3.5 w-3.5" />
                  <span class="font-semibold">#{{ sug.invoice_number }}</span>
                  <span class="text-gray-700">{{ sug.member_name }}</span>
                  <span class="text-xs text-gray-500">· {{ sug.price_item_name || '—' }}</span>
                  <span class="text-xs font-semibold text-gray-800">{{ formatNOK(sug.total_amount) }}</span>
                </p>
                <p class="text-xs text-gray-500" :title="sug.why_tooltip">
                  {{ t('admin.bankReconcile.confidence.' + sug.confidence_label) }} · {{ sug.why_tooltip }}
                  <span v-if="sug.kid_number" class="ml-1 font-mono">KID: {{ sug.kid_number }}</span>
                </p>
              </div>
              <button
                type="button"
                class="rounded-md bg-brand-600 px-3 py-1 text-xs font-semibold text-white hover:bg-brand-700"
                @click="doAssignInvoice(row.id, sug, { state: panelAlias, offered: panelAliasOffered })"
              >
                {{ t('admin.bankReconcile.assign') }}
              </button>
            </div>

            <!-- Footer actions -->
            <div class="flex flex-wrap gap-2">
              <button
                type="button"
                class="inline-flex items-center gap-1 rounded-md border border-gray-300 bg-white px-3 py-1 text-xs font-semibold text-gray-700 hover:bg-gray-50"
                @click="openPotential(row)"
              >
                <Search class="h-3 w-3" />
                {{ t('admin.bankReconcile.searchInvoice') }}
              </button>
              <button
                type="button"
                class="inline-flex items-center gap-1 rounded-md border border-gray-300 bg-white px-3 py-1 text-xs font-semibold text-gray-700 hover:bg-gray-50"
                @click="openAccountPicker(row)"
              >
                <FileText class="h-3 w-3" />
                {{ t('admin.bankReconcile.lagBilag') }}
              </button>
              <button
                v-if="!row.dismissed_at"
                type="button"
                class="inline-flex items-center gap-1 rounded-md border border-amber-300 bg-white px-3 py-1 text-xs font-semibold text-amber-700 hover:bg-amber-50"
                @click="openDismiss(row)"
              >
                <Ban class="h-3 w-3" />
                {{ t('admin.bankReconcile.dismiss') }}
              </button>
              <button
                v-if="row.dismissed_at"
                type="button"
                class="inline-flex items-center gap-1 rounded-md border border-gray-300 bg-white px-3 py-1 text-xs font-semibold text-gray-700 hover:bg-gray-50"
                @click="doUnassign(row.id)"
              >
                <Undo2 class="h-3 w-3" />
                {{ t('admin.bankReconcile.unassign') }}
              </button>
            </div>
          </div>
        </li>
      </ul>
    </template>

    <!-- Multi-select action bar -->
    <Transition
      enter-active-class="transition duration-150 ease-out"
      enter-from-class="translate-y-4 opacity-0"
      leave-active-class="transition duration-100 ease-in"
      leave-to-class="translate-y-4 opacity-0"
    >
      <div
        v-if="selectedRows.length > 0"
        class="fixed bottom-6 left-1/2 z-50 -translate-x-1/2 flex items-center gap-3 rounded-xl border border-gray-200 bg-white px-5 py-3 shadow-xl"
      >
        <CheckSquare class="h-4 w-4 text-brand-600" />
        <span class="text-sm font-semibold text-gray-800">
          {{ selectedRows.length }} {{ selectedRows.length === 1 ? t('admin.bankReconcile.rowSingular') : t('admin.bankReconcile.rowPlural') }}
          · {{ formatNOK(selectedTotal) }}
        </span>
        <span class="h-4 w-px bg-gray-300" />
        <button
          type="button"
          class="inline-flex items-center gap-1.5 rounded-md bg-gray-100 px-3 py-1.5 text-xs font-semibold text-gray-700 hover:bg-gray-200"
          @click="openAccountPicker(selectedRows[0])"
        >
          <FileText class="h-3.5 w-3.5" />
          {{ t('admin.bankReconcile.lagBilag') }}
        </button>
        <button
          v-if="selectedRows.length >= 2"
          type="button"
          class="inline-flex items-center gap-1.5 rounded-md bg-brand-600 px-3 py-1.5 text-xs font-semibold text-white hover:bg-brand-700"
          @click="openMultiInvoice()"
        >
          <Search class="h-3.5 w-3.5" />
          {{ t('admin.bankReconcile.assignInvoice') }}
        </button>
        <button
          type="button"
          class="text-xs text-gray-400 hover:text-gray-600"
          @click="clearSelection()"
        >
          ✕
        </button>
      </div>
    </Transition>

    <!-- Potential invoices modal -->
    <Modal v-model:open="potentialOpen" size="2xl" :title="t('admin.bankReconcile.potentialTitle')">
      <div class="rounded-md bg-brand-50 px-3 py-2 text-xs text-brand-900">
        {{ t('admin.bankReconcile.potentialBanner') }}
      </div>
      <PayerAliasToggle
        v-if="potentialRowId && potentialAliasOffered && potentialAlias"
        class="mt-3"
        :model-value="rememberFor(potentialRowId, potentialAlias)"
        :payer="potentialAlias.payer"
        :taken="potentialAlias.taken"
        @update:model-value="potentialRowId && setRemember(potentialRowId, $event)"
      />
      <input
        v-model="potentialQuery"
        type="search"
        :placeholder="t('admin.bankReconcile.searchInvoicePlaceholder')"
        class="mt-3 w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
        @input="loadPotential"
      />
      <p v-if="potentialLoading" class="mt-3 text-xs text-gray-500">{{ t('common.loading') }}…</p>
      <SuggestionsLoadError v-else-if="potentialError" @retry="loadPotential" />
      <ul v-else-if="potentialItems.length" class="mt-3 max-h-96 divide-y divide-gray-100 overflow-y-auto">
        <li v-for="inv in potentialItems" :key="inv.invoice_id" class="flex items-center justify-between gap-3 py-2 text-sm">
          <div>
            <p class="font-semibold">#{{ inv.invoice_number }} <span class="font-normal text-gray-700">— {{ inv.member_name }}</span> <span class="text-sm font-semibold text-gray-800">{{ formatNOK(inv.total_amount) }}</span></p>
            <p class="text-xs text-gray-500">{{ inv.price_item_name || '—' }} · {{ inv.member_email || '—' }} · {{ formatDate(inv.issue_date) }}<span v-if="inv.kid_number" class="ml-1 font-mono">· KID: {{ inv.kid_number }}</span></p>
          </div>
          <button
            type="button"
            class="rounded-md bg-brand-600 px-3 py-1 text-xs font-semibold text-white hover:bg-brand-700"
            @click="potentialRowId && doAssignInvoice(potentialRowId, inv, { state: potentialAlias, offered: potentialAliasOffered })"
          >
            {{ t('admin.bankReconcile.assign') }}
          </button>
        </li>
      </ul>
      <p v-else class="mt-3 text-xs text-gray-500">{{ t('admin.bankReconcile.noPotential') }}</p>
    </Modal>

    <!-- Lag bilag modal -->
    <Modal v-model:open="accountPickerOpen" size="lg" :title="t('admin.bankReconcile.accountPickerTitle')">
      <label class="block text-xs font-semibold text-gray-700">{{ t('admin.bankReconcile.bilagDescriptionLabel') }}</label>
      <textarea
        v-model="bilagDescription"
        rows="2"
        :placeholder="t('admin.bankReconcile.bilagDescriptionPlaceholder')"
        class="mt-1 w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
      />

      <template v-if="activeSource === 'bank'">
        <p v-if="partnerNotice" class="mt-3 rounded-md bg-amber-50 px-3 py-2 text-xs text-amber-800" data-testid="partner-notice">
          {{ partnerNotice }}
        </p>
        <BankRowPartnerChoice
          v-if="singlePick"
          v-model="partnerChoice"
          :partners="singlePartners"
          :loading="partnersLoading"
        />
        <template v-else-if="!partnersLoading">
          <p v-if="selectionIsGroup" class="mt-3 rounded-md bg-indigo-50 px-3 py-2 text-xs text-indigo-900" data-testid="partner-group-banner">
            {{ t('admin.bankReconcile.partners.groupBanner', { count: pickerRows.length, net: formatNOK(groupNet) }) }}
          </p>
          <p v-else-if="selectionPartlyPaired" class="mt-3 rounded-md bg-amber-50 px-3 py-2 text-xs text-amber-800">
            {{ t('admin.bankReconcile.partners.bulkPartlyPaired') }}
          </p>
          <div v-if="strongOutside.length && !selectionPartlyPaired" class="mt-3 rounded-md border border-indigo-300 bg-indigo-50 p-3 text-xs" data-testid="partner-outside">
            <p class="font-semibold text-indigo-900">{{ t('admin.bankReconcile.partners.bulkOutside') }}</p>
            <ul class="mt-1 space-y-0.5 text-gray-700">
              <li v-for="o in strongOutside" :key="o.row.id + o.partner.id">
                {{ formatNOK(o.row.amount) }} · {{ formatDate(o.row.row_date) }} ↔ {{ formatNOK(o.partner.amount) }} · {{ formatDate(o.partner.row_date) }}
              </li>
            </ul>
            <label class="mt-2 flex items-center gap-2 text-gray-800">
              <input v-model="ackSeparate" type="checkbox" data-testid="partner-ack-separate" />
              {{ t('admin.bankReconcile.partners.bulkAck') }}
            </label>
          </div>
        </template>
        <p v-if="groupRowIds" class="mt-3 text-xs text-gray-600">
          {{ t('admin.bankReconcile.partners.groupAccountsHint', { net: formatNOK(groupNet) }) }}
        </p>
      </template>

      <input
        v-model="accountPickerSearch"
        type="search"
        :placeholder="t('admin.bankReconcile.searchAccountPlaceholder')"
        class="mt-3 w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
      />

      <!-- Booked together: every non-bank account, grouped by type. -->
      <div v-if="groupRowIds" class="mt-3 max-h-72 overflow-y-auto" data-testid="group-accounts">
        <div v-for="sec in groupAccountSections" :key="sec.type" class="mb-2">
          <p class="text-xs font-semibold uppercase text-gray-500">{{ t('admin.bankReconcile.partners.accountTypes.' + sec.type) }}</p>
          <ul class="divide-y divide-gray-100">
            <li v-for="a in sec.accounts" :key="a.code" class="flex items-center justify-between gap-3 py-2 text-sm">
              <AccountOptionLabel :code="a.code" :name="a.name" />
              <button
                type="button"
                :disabled="!!assignBlockedReason"
                class="rounded-md bg-brand-600 px-3 py-1 text-xs font-semibold text-white hover:bg-brand-700 disabled:cursor-not-allowed disabled:opacity-40"
                :data-testid="`assign-group-${a.code}`"
                @click="accountPickerRowId && doAssignAccount(accountPickerRowId, a.code, accountPickerKind)"
              >
                {{ t('admin.bankReconcile.partners.assignTogether') }}
              </button>
            </li>
          </ul>
        </div>
      </div>
      <template v-else>
        <div v-if="accountPickerSuggestions.length" class="mt-3">
          <p class="text-xs font-semibold uppercase text-gray-500">{{ t('admin.bankReconcile.accountSuggestions') }}</p>
          <ul class="mt-1 space-y-1">
            <li
              v-for="sug in accountPickerSuggestions"
              :key="sug.code"
              class="flex items-center justify-between gap-3 rounded-md border px-3 py-2 text-sm"
              :class="confidenceClass[sug.confidence_label]"
              :title="sug.why_tooltip"
              :data-testid="`account-suggestion-${sug.code}`"
            >
              <div class="min-w-0 flex-1">
                <AccountOptionLabel :code="sug.code" :name="sug.name" />
                <p v-if="sug.reason === 'price_item'" class="text-xs text-gray-700" data-testid="price-match-why">
                  {{ t('admin.bankReconcile.confidence.' + sug.confidence_label) }} ·
                  {{ t('admin.bankReconcile.priceMatchWhy', { items: sug.why_tooltip }) }}
                </p>
              </div>
              <button
                type="button"
                :disabled="!!assignBlockedReason"
                class="rounded-md bg-brand-600 px-3 py-1 text-xs font-semibold text-white hover:bg-brand-700 disabled:cursor-not-allowed disabled:opacity-40"
                @click="accountPickerRowId && doAssignAccount(accountPickerRowId, sug.code, accountPickerKind)"
              >
                {{ t('admin.bankReconcile.assign') }}
              </button>
            </li>
          </ul>
        </div>
        <ul class="mt-3 max-h-72 divide-y divide-gray-100 overflow-y-auto">
          <li v-for="a in filteredAccounts" :key="a.code" class="flex items-center justify-between gap-3 py-2 text-sm">
            <AccountOptionLabel :code="a.code" :name="a.name" />
            <button
              type="button"
              :disabled="!!assignBlockedReason"
              class="rounded-md bg-brand-600 px-3 py-1 text-xs font-semibold text-white hover:bg-brand-700 disabled:cursor-not-allowed disabled:opacity-40"
              :data-testid="`assign-account-${a.code}`"
              @click="accountPickerRowId && doAssignAccount(accountPickerRowId, a.code, accountPickerKind)"
            >
              {{ t('admin.bankReconcile.assign') }}
            </button>
          </li>
        </ul>
      </template>
    </Modal>

    <!-- Dismiss reason modal -->
    <Modal v-model:open="dismissOpen" size="md" :title="t('admin.bankReconcile.dismissTitle')">
      <p class="text-sm text-gray-700">{{ t('admin.bankReconcile.dismissHint') }}</p>
      <fieldset class="mt-4 space-y-2">
        <label v-for="r in DISMISS_REASONS" :key="r" class="flex cursor-pointer items-start gap-2 rounded-md border border-gray-200 p-3 hover:bg-gray-50">
          <input v-model="dismissReason" type="radio" :value="r" class="mt-0.5" />
          <div>
            <p class="text-sm font-medium">{{ t('admin.bankReconcile.reasons.' + r) }}</p>
            <p class="text-xs text-gray-500">{{ t('admin.bankReconcile.reasonHints.' + r) }}</p>
          </div>
        </label>
      </fieldset>
      <template #footer>
        <div class="flex justify-end gap-2">
          <button type="button" class="rounded-md bg-white px-3 py-2 text-sm font-semibold text-gray-700 ring-1 ring-gray-300 hover:bg-gray-50" @click="dismissOpen = false">
            {{ t('common.cancel') }}
          </button>
          <button type="button" :disabled="!dismissReason" class="rounded-md bg-amber-600 px-3 py-2 text-sm font-semibold text-white hover:bg-amber-700 disabled:opacity-50" @click="submitDismiss">
            {{ t('admin.bankReconcile.dismiss') }}
          </button>
        </div>
      </template>
    </Modal>
    <!-- Multi-row assign invoice modal -->
    <Modal v-model:open="multiInvoiceOpen" size="2xl" :title="t('admin.bankReconcile.multiInvoiceTitle')">
      <div class="rounded-md bg-brand-50 px-3 py-2 text-xs text-brand-900">
        {{ t('admin.bankReconcile.multiInvoiceBanner', { total: formatNOK(selectedTotal), count: selectedRows.length }) }}
      </div>
      <PayerAliasToggle
        v-if="multiAliasOffered && multiAlias"
        v-model="multiRemember"
        class="mt-3"
        :payer="multiAlias.payer"
        :taken="multiAlias.taken"
      />
      <input
        v-model="multiInvoiceQuery"
        type="search"
        :placeholder="t('admin.bankReconcile.searchInvoicePlaceholder')"
        class="mt-3 w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
        @input="loadMultiInvoice"
      />
      <p v-if="multiInvoiceLoading" class="mt-3 text-xs text-gray-500">{{ t('common.loading') }}…</p>
      <SuggestionsLoadError v-else-if="multiInvoiceError" @retry="loadMultiInvoice" />
      <ul v-else-if="multiInvoiceItems.length" class="mt-3 max-h-96 divide-y divide-gray-100 overflow-y-auto">
        <li v-for="inv in multiInvoiceItems" :key="inv.invoice_id" class="flex items-center justify-between gap-3 py-2 text-sm">
          <div class="min-w-0 flex-1">
            <p class="font-semibold">#{{ inv.invoice_number }} <span class="font-normal text-gray-700">— {{ inv.member_name }}</span> <span class="text-sm font-semibold text-gray-800">{{ formatNOK(inv.total_amount) }}</span></p>
            <p class="text-xs text-gray-500">{{ inv.price_item_name || '—' }} · {{ formatDate(inv.issue_date) }}<span v-if="inv.kid_number" class="ml-1 font-mono">· KID: {{ inv.kid_number }}</span></p>
          </div>
          <button
            type="button"
            :disabled="Math.round(selectedTotal * 100) !== Math.round(inv.total_amount * 100)"
            class="rounded-md px-3 py-1 text-xs font-semibold text-white disabled:cursor-not-allowed disabled:opacity-40"
            :class="Math.round(selectedTotal * 100) === Math.round(inv.total_amount * 100) ? 'bg-brand-600 hover:bg-brand-700' : 'bg-gray-400'"
            :title="Math.round(selectedTotal * 100) !== Math.round(inv.total_amount * 100) ? t('admin.bankReconcile.multiInvoiceMismatch', { inv: formatNOK(inv.total_amount), sel: formatNOK(selectedTotal) }) : ''"
            @click="doAssignMultiInvoice(inv)"
          >
            {{ t('admin.bankReconcile.assign') }}
          </button>
        </li>
      </ul>
      <p v-else class="mt-3 text-xs text-gray-500">{{ t('admin.bankReconcile.noPotential') }}</p>
    </Modal>

    <StripePayoutPicker v-if="stripePickerRow" v-model:open="stripePickerOpen" :row="stripePickerRow" />
  </div>
</template>
