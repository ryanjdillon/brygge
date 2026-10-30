<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useConfirm, useConfirmStore } from '@/stores/confirm'
import { useFreshTotp } from '@/composables/useFreshTotp'
import { useCan } from '@/composables/useCan'
import {
  Plus,
  ChevronDown,
  ChevronUp,
  ChevronRight,
  Send,
  Ban,
  Trash2,
  RefreshCw,
  Lock,
  Unlock,
  Search,
  Paperclip,
} from 'lucide-vue-next'
import {
  useFiscalPeriods,
  useJournalEntries,
  usePostEntry,
  useVoidEntry,
  useDeleteDraftEntry,
  useCreatePeriod,
  useClosePeriod,
  useReopenPeriod,
  useSetPeriodReference,
  defaultPeriodId,
  useSyncPayments,
  useSyncInvoices,
  previewResplitInvoiceBilags,
  useApplyResplitInvoiceBilags,
  previewVippsDraftBacklog,
  useApplyVippsDraftBacklog,
  useReleaseVippsBacklogCharge,
  fetchJournalAttachmentUrl,
  type JournalEntry,
  type SkippedItem,
  type SyncResult,
  type ResplitResult,
  type VippsBacklogResult,
  type VippsBacklogRelease,
} from '@/composables/useAccounting'
import Select from '@/components/ui/form/Select.vue'
import NumberInput from '@/components/ui/form/NumberInput.vue'
import VippsFallbackReclass from '@/components/admin/VippsFallbackReclass.vue'
import { ApiError } from '@/lib/errors'
import { asClearedSettlementRefusal, type ClearedSettlementRefusal } from '@/composables/useClearedSettlement'
import ClearedSettlementRefusalNote from '@/components/admin/ClearedSettlementRefusal.vue'
import { isReferencePeriodError } from '@/composables/useBankReconcile'

const { t } = useI18n()
// Periods are a ledger write; anything that books or voids an entry is a
// money move. Board members without those roles read the journal (BRY-372).
const { can } = useCan()
const canEditPeriods = can('ledger')
const canBook = can('money')

const { data: periods } = useFiscalPeriods()

const selectedPeriodId = ref('')
const statusFilter = ref('all')
const sourceFilter = ref('all')
const searchQ = ref('')
const sortBy = ref('entry_number')
const sortDir = ref<'asc' | 'desc'>('desc')
const expandedId = ref<string | null>(null)
const syncing = ref(false)
const syncMessage = ref('')
const syncSkipped = ref<SkippedItem[]>([])
// Release offers belong to the period the backlog preview ran for.
const vippsReleasable = ref<VippsBacklogRelease[]>([])
watch(selectedPeriodId, () => { vippsReleasable.value = [] })
const showCreateYear = ref(false)

function toggleSort(col: string) {
  if (sortBy.value === col) {
    sortDir.value = sortDir.value === 'desc' ? 'asc' : 'desc'
  } else {
    sortBy.value = col
    sortDir.value = 'desc'
  }
}

const nextYear = computed(() => {
  if (!periods.value?.length) return new Date().getFullYear()
  const maxYear = Math.max(...periods.value.map(p => p.year))
  return maxYear + 1
})
const newYear = ref<number | null>(0)
watch(nextYear, (val) => { newYear.value = val }, { immediate: true })

watch(periods, (val) => {
  if (val && val.length > 0 && !selectedPeriodId.value) {
    selectedPeriodId.value = defaultPeriodId(val)
  }
}, { immediate: true })

const selectedPeriod = computed(() =>
  periods.value?.find(p => p.id === selectedPeriodId.value),
)
// A reference year books costs only; the buttons that book or correct whole
// periods would be refused, so they are not offered.
const isReference = computed(() => !!selectedPeriod.value?.reference_only)

const hasPeriods = computed(() => (periods.value?.length ?? 0) > 0)

const { data: entries, isLoading } = useJournalEntries(selectedPeriodId, {
  status: statusFilter,
  source: sourceFilter,
  q: searchQ,
  sortBy,
  sortDir,
})

const postMutation = usePostEntry()
const voidMutation = useVoidEntry()
const deleteDraftMutation = useDeleteDraftEntry()
const createPeriodMutation = useCreatePeriod()
const closePeriodMutation = useClosePeriod()
const reopenPeriodMutation = useReopenPeriod()
const setReferenceMutation = useSetPeriodReference()
const syncPaymentsMutation = useSyncPayments()
const syncInvoicesMutation = useSyncInvoices()
const applyResplitMutation = useApplyResplitInvoiceBilags()
const applyVippsBacklogMutation = useApplyVippsDraftBacklog()
const releaseVippsChargeMutation = useReleaseVippsBacklogCharge()
const { ensureFreshTotp } = useFreshTotp()
const askConfirm = useConfirm()
const confirmStore = useConfirmStore()

const statusLabels = computed<Record<string, string>>(() => ({
  draft: t('admin.accounting.journal.draft'),
  posted: t('admin.accounting.journal.posted'),
  voided: t('admin.accounting.journal.voided'),
}))

const statusColors: Record<string, string> = {
  draft: 'bg-yellow-100 text-yellow-800',
  posted: 'bg-green-100 text-green-800',
  voided: 'bg-red-100 text-red-800',
}

const sourceLabels = computed<Record<string, string>>(() => ({
  manual: t('admin.accounting.journal.sourceManual'),
  sync_payment: t('admin.accounting.journal.sourcePaymentSync'),
  sync_invoice: t('admin.accounting.journal.sourceInvoiceSync'),
  bank_import: t('admin.accounting.journal.sourceBankImport'),
}))

const accountTypeColors: Record<string, string> = {
  asset: 'bg-brand-100 text-brand-800',
  liability: 'bg-amber-100 text-amber-800',
  revenue: 'bg-green-100 text-green-800',
  expense: 'bg-red-100 text-red-800',
}

function toggleExpand(entry: JournalEntry) {
  expandedId.value = expandedId.value === entry.id ? null : entry.id
}

// What a failed booking says. A reference year refuses everything but costs
// with a typed 409, worded here in the viewer's language.
function bookingError(err: unknown): string {
  return `${t('common.error')}: ${isReferencePeriodError(err) ? t('admin.accounting.referencePeriod') : (err as Error).message}`
}

async function handlePost(entryId: string) {
  if (!(await ensureFreshTotp())) return
  syncMessage.value = ''
  postMutation.mutate(entryId, {
    onError: (err) => {
      syncMessage.value = bookingError(err)
    },
  })
}

// An entry booked from a Stripe row (409 stripe_entry) or one a bank row
// or Stripe payout points at (409 bank_entry) is refused here: it is undone
// from the Stripe row or from Tildel, which also release what it settled,
// so the refusal links there.
type VoidRefusal = 'stripe_entry' | 'bank_entry'
const VOID_REFUSALS: Record<VoidRefusal, { text: string; link: string; to: string }> = {
  stripe_entry: {
    text: 'admin.accounting.journal.voidStripeEntry',
    link: 'admin.accounting.journal.voidStripeEntryLink',
    to: '/admin/accounting/bank-imports?stripe=booked',
  },
  bank_entry: {
    text: 'admin.accounting.journal.voidBankEntry',
    link: 'admin.accounting.journal.voidBankEntryLink',
    to: '/admin/accounting/bank-imports?tab=reconcile',
  },
}
const voidRefusal = ref<VoidRefusal | null>(null)
// A Vipps charge entry whose settlement is already cleared is refused too
// (409 charge_in_cleared_settlement); the note offers to undo the clearing and
// then voids the entry again.
const clearedRefusal = ref<{ entryId: string; refusal: ClearedSettlementRefusal } | null>(null)

function runVoid(entryId: string) {
  voidRefusal.value = null
  clearedRefusal.value = null
  syncMessage.value = ''
  voidMutation.mutate(entryId, {
    onError: (err) => {
      if (err instanceof ApiError && err.status === 409 && (err.code === 'stripe_entry' || err.code === 'bank_entry')) {
        voidRefusal.value = err.code
        return
      }
      const cleared = asClearedSettlementRefusal(err)
      if (cleared) {
        clearedRefusal.value = { entryId, refusal: cleared }
        return
      }
      syncMessage.value = bookingError(err)
    },
  })
}

async function handleVoid(entryId: string) {
  if (!(await ensureFreshTotp())) return
  if (confirm(t('admin.accounting.journal.confirmVoid'))) runVoid(entryId)
}

function voidAgain() {
  const entryId = clearedRefusal.value?.entryId
  clearedRefusal.value = null
  if (entryId) runVoid(entryId)
}

// A draft blocks closing its period, so an abandoned one needs a way out.
// Drafts booked from a bank or Vipps row are refused by the server (undo
// those from Tildel instead).
async function handleDeleteDraft(entryId: string) {
  if (!(await ensureFreshTotp())) return
  const ok = await askConfirm({
    title: t('admin.accounting.journal.deleteDraftTitle'),
    body: t('admin.accounting.journal.deleteDraftBody'),
    confirmLabel: t('admin.accounting.journal.deleteDraft'),
    cancelLabel: t('common.cancel'),
    tone: 'danger',
  })
  if (!ok) return
  syncMessage.value = ''
  deleteDraftMutation.mutate(entryId, {
    onError: (err) => {
      syncMessage.value = `${t('common.error')}: ${(err as Error).message}`
    },
  })
}

async function openAttachment(entryId: string) {
  try {
    const url = await fetchJournalAttachmentUrl(entryId)
    window.open(url, '_blank', 'noopener,noreferrer')
  } catch {
    // silently ignore — attachment may not be accessible
  }
}

async function handleSync() {
  if (!selectedPeriodId.value) return
  if (!(await ensureFreshTotp())) return
  syncing.value = true
  syncMessage.value = ''
  syncSkipped.value = []
  vippsReleasable.value = []

  try {
    const payResult = await new Promise<SyncResult>((resolve, reject) => {
      syncPaymentsMutation.mutate(
        { period_id: selectedPeriodId.value },
        { onSuccess: resolve, onError: reject },
      )
    })

    const invResult = await new Promise<SyncResult>((resolve, reject) => {
      syncInvoicesMutation.mutate(
        { period_id: selectedPeriodId.value },
        { onSuccess: resolve, onError: reject },
      )
    })

    syncMessage.value = `${t('admin.accounting.journal.updateComplete')}: ${payResult.synced} ${t('admin.accounting.journal.paymentsSynced')}, ${invResult.synced} ${t('admin.accounting.journal.invoicesSynced')}`
    if (invResult.reversed) {
      syncMessage.value += `, ${invResult.reversed} ${t('admin.accounting.journal.invoicesReversed')}`
    }
    syncSkipped.value = [...(payResult.skipped_details ?? []), ...(invResult.skipped_details ?? [])]
  } catch (err) {
    syncMessage.value = `${t('common.error')}: ${(err as Error).message}`
  } finally {
    syncing.value = false
  }
}

// Corrects the revenue split of booked fakturas with reclassification
// entries; the original bilags are never edited or deleted.
const resplitting = ref(false)
async function handleResplitInvoiceBilags() {
  if (!selectedPeriodId.value) return
  if (!(await ensureFreshTotp())) return
  const periodLabel = periods.value?.find((p) => p.id === selectedPeriodId.value)?.year ?? ''

  resplitting.value = true
  syncMessage.value = ''
  syncSkipped.value = []
  vippsReleasable.value = []
  try {
    const preview = await previewResplitInvoiceBilags(selectedPeriodId.value)
    syncSkipped.value = preview.skipped_details ?? []
    if (!preview.invoices.length) {
      syncMessage.value = t('admin.accounting.journal.resplitNothing')
      return
    }
    const ok = await askConfirm({
      title: t('admin.accounting.journal.resplitTitle', { year: periodLabel }),
      body: t('admin.accounting.journal.resplitBody', { count: preview.invoices.length }),
      details: preview.account_totals.map(
        (m) => `${m.account_code}: ${m.amount > 0 ? '+' : ''}${m.amount.toLocaleString('nb-NO')} kr`,
      ),
      confirmLabel: t('admin.accounting.journal.resplitConfirm'),
      cancelLabel: t('common.cancel'),
      tone: 'warning',
    })
    if (!ok) return

    const result = await new Promise<ResplitResult>((resolve, reject) => {
      applyResplitMutation.mutate(
        { period_id: selectedPeriodId.value },
        { onSuccess: resolve, onError: reject },
      )
    })
    syncMessage.value = t('admin.accounting.journal.resplitDone', { posted: result.posted })
    syncSkipped.value = result.skipped_details ?? []
  } catch (err) {
    syncMessage.value = `${t('common.error')}: ${(err as Error).message}`
  } finally {
    resplitting.value = false
  }
}

// Posts the period's Vipps drafts left over from before postings were
// booked on creation. Parked charges are reclassified first when the club
// has auto-assign on; unsafe drafts are refused and listed.
const postingVippsDrafts = ref(false)
async function handlePostVippsDrafts() {
  if (!selectedPeriodId.value) return
  if (!(await ensureFreshTotp())) return
  const periodLabel = periods.value?.find((p) => p.id === selectedPeriodId.value)?.year ?? ''

  postingVippsDrafts.value = true
  syncMessage.value = ''
  syncSkipped.value = []
  vippsReleasable.value = []
  try {
    const preview = await previewVippsDraftBacklog(selectedPeriodId.value)
    syncSkipped.value = preview.refused ?? []
    vippsReleasable.value = preview.releasable ?? []
    if (!preview.posted && !preview.reclassified.length) {
      syncMessage.value = t('admin.accounting.journal.vippsDraftsNothing')
      return
    }
    const details = preview.posted_totals.map(
      (a) => `${a.account_code}: ${t('admin.accounting.journal.vippsDraftsDebit')} ${a.debit.toLocaleString('nb-NO')} · ${t('admin.accounting.journal.vippsDraftsCredit')} ${a.credit.toLocaleString('nb-NO')} kr`,
    )
    const body = [
      t('admin.accounting.journal.vippsDraftsBody', {
        posted: preview.posted,
        reclassified: preview.reclassified.length,
        refused: preview.refused.length,
      }),
      preview.auto_assign_enabled ? '' : t('admin.accounting.journal.vippsDraftsAutoAssignOff'),
    ]
      .filter(Boolean)
      .join(' ')
    const ok = await askConfirm({
      title: t('admin.accounting.journal.vippsDraftsTitle', { year: periodLabel }),
      body,
      details,
      // One tick per rebooking, so a wrong one can be left out without
      // cancelling the whole batch.
      checklist: preview.reclassified.length
        ? {
            label: t('admin.accounting.journal.vippsDraftsChecklist'),
            items: preview.reclassified.map((r) => ({
              value: r.entry_id,
              label: `${r.date} ${r.amount.toLocaleString('nb-NO')} kr: ${r.from} → ${r.to}`,
            })),
          }
        : undefined,
      confirmLabel: t('admin.accounting.journal.vippsDraftsConfirm'),
      cancelLabel: t('common.cancel'),
      tone: 'warning',
    })
    if (!ok) return
    // Always send the list, with the target each row showed (empty when the
    // preview had no rebookings): the apply then books only what the
    // treasurer saw, never a draft that became reclassifiable, or moved to
    // another target, after the preview.
    const ticked = preview.reclassified.length ? [...confirmStore.checklistValues] : []
    const targets = Object.fromEntries(preview.reclassified.map((r) => [r.entry_id, r.to]))

    const result = await new Promise<VippsBacklogResult>((resolve, reject) => {
      applyVippsBacklogMutation.mutate(
        {
          period_id: selectedPeriodId.value,
          reclassify_entry_ids: ticked,
          reclassify_targets: Object.fromEntries(ticked.map((id) => [id, targets[id] ?? ''])),
        },
        { onSuccess: resolve, onError: reject },
      )
    })
    const payouts = result.payouts
    syncMessage.value = [
      t('admin.accounting.journal.vippsDraftsDone', {
        posted: result.posted,
        reclassified: result.reclassified.length,
      }),
      result.unchecked || result.stale_ids?.length
        ? t('admin.accounting.journal.vippsDraftsSkipped', {
            unchecked: result.unchecked ?? 0,
            stale: result.stale_ids?.length ?? 0,
          })
        : '',
      payouts
        ? t('admin.bankImports.payoutSweepCounts', {
            cleared: payouts.cleared.length,
            waiting: payouts.waiting.length,
            skipped: payouts.skipped.length,
            failed: payouts.failed.length,
          })
        : '',
    ]
      .filter(Boolean)
      .join(' ')
    syncSkipped.value = [
      ...(result.refused ?? []),
      ...[...(payouts?.skipped ?? []), ...(payouts?.failed ?? [])].map((p) => ({
        ref: `Utb. ${p.settlement_number}`,
        reason: p.reason ?? '',
      })),
    ]
    vippsReleasable.value = result.releasable ?? []
  } catch (err) {
    syncMessage.value = bookingError(err)
  } finally {
    postingVippsDrafts.value = false
  }
}

// Releases a refused 1500-parked charge back to the Vipps queue so it can be
// assigned by hand. Only charges the server listed as releasable are offered.
const releasingRowId = ref('')
async function handleReleaseVippsCharge(item: VippsBacklogRelease) {
  if (!(await ensureFreshTotp())) return
  const ok = await askConfirm({
    title: t('admin.accounting.journal.vippsDraftsReleaseTitle', { ref: item.ref }),
    body: t('admin.accounting.journal.vippsDraftsReleaseBody', {
      amount: item.amount.toLocaleString('nb-NO'),
      date: item.date,
    }),
    confirmLabel: t('admin.accounting.journal.vippsDraftsRelease'),
    cancelLabel: t('common.cancel'),
    tone: 'warning',
  })
  if (!ok) return
  releasingRowId.value = item.row_id
  syncMessage.value = ''
  releaseVippsChargeMutation.mutate(item.row_id, {
    onSuccess: () => {
      vippsReleasable.value = vippsReleasable.value.filter((r) => r.row_id !== item.row_id)
      syncSkipped.value = syncSkipped.value.filter((s) => s.ref !== item.ref)
      syncMessage.value = t('admin.accounting.journal.vippsDraftsReleased', { ref: item.ref })
    },
    onError: (err) => {
      syncMessage.value = `${t('common.error')}: ${(err as Error).message}`
    },
    onSettled: () => {
      releasingRowId.value = ''
    },
  })
}

function handleCreatePeriod() {
  if (newYear.value == null) return
  createPeriodMutation.mutate({ year: newYear.value }, {
    onSuccess: (period) => {
      selectedPeriodId.value = period.id
      showCreateYear.value = false
      newYear.value = nextYear.value
      handleSync()
    },
  })
}

function handleClosePeriod() {
  if (!selectedPeriodId.value) return
  syncMessage.value = ''
  closePeriodMutation.mutate(selectedPeriodId.value, {
    onError: (err) => {
      syncMessage.value = `${t('common.error')}: ${t('admin.accounting.journal.closeBlocked')} (${(err as Error).message})`
    },
  })
}

function handleReopenPeriod() {
  if (!selectedPeriodId.value) return
  reopenPeriodMutation.mutate(selectedPeriodId.value)
}

// The toggle's 409 codes and the reason line each one shares.
const REFERENCE_REFUSALS: Record<string, string> = {
  reference_current_year: 'current_year',
  reference_has_opening_balance: 'opening_balance',
  period_closed: 'period_closed',
}

// Marking a year as reference changes what gets booked and what later years
// carry, so the step-up is asked for at click time, before the confirm.
async function handleToggleReference() {
  const period = selectedPeriod.value
  if (!period) return
  if (!(await ensureFreshTotp())) return
  const marking = !period.reference_only
  const prefix = marking ? 'admin.accounting.periods.markReference' : 'admin.accounting.periods.clearReference'
  const body = [
    t(`${prefix}Body`, { year: period.year }),
    marking && period.open_invoice_bookings > 0
      ? t('admin.accounting.periods.markReferenceInvoices', { count: period.open_invoice_bookings })
      : '',
  ]
    .filter(Boolean)
    .join(' ')
  const ok = await askConfirm({
    title: t(`${prefix}Title`, { year: period.year }),
    body,
    confirmLabel: t(`${prefix}Confirm`),
    cancelLabel: t('common.cancel'),
    tone: 'warning',
  })
  if (!ok) return
  syncMessage.value = ''
  setReferenceMutation.mutate(
    { periodId: period.id, referenceOnly: marking },
    {
      onError: (err) => {
        // The server's refusals have wording of their own in the viewer's language.
        const blocked = err instanceof ApiError ? REFERENCE_REFUSALS[err.code ?? ''] : undefined
        syncMessage.value = `${t('common.error')}: ${
          blocked ? t(`admin.accounting.periods.referenceBlocked.${blocked}`) : (err as Error).message
        }`
      },
    },
  )
}


const periodOptions = computed(() =>
  (periods.value ?? []).map((p) => ({
    value: p.id,
    label: p.reference_only ? t('admin.accounting.periods.referenceLabel', { year: p.year }) : String(p.year),
  })),
)

const statusFilterOptions = computed(() => [
  { value: 'all', label: t('admin.accounting.journal.allStatuses') },
  { value: 'draft', label: t('admin.accounting.journal.draft') },
  { value: 'posted', label: t('admin.accounting.journal.posted') },
  { value: 'voided', label: t('admin.accounting.journal.voided') },
])

const sourceFilterOptions = computed(() => [
  { value: 'all', label: t('admin.accounting.journal.allSources') },
  { value: 'manual', label: t('admin.accounting.journal.sourceManual') },
  { value: 'sync_payment', label: t('admin.accounting.journal.sourcePaymentSync') },
  { value: 'sync_invoice', label: t('admin.accounting.journal.sourceInvoiceSync') },
  { value: 'bank_import', label: t('admin.accounting.journal.sourceBankImport') },
])

function formatNOK(amount: number): string {
  if (!amount) return '-'
  return new Intl.NumberFormat('nb-NO', { minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(amount)
}
</script>

<template>
  <div>
    <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.accounting.journal.title') }}</h1>

    <!-- Top bar: period management -->
    <div class="mt-4 rounded-lg border border-gray-200 bg-white p-4">
      <div v-if="!hasPeriods" class="flex flex-wrap items-center gap-3">
        <p class="text-sm text-gray-500">{{ t('admin.accounting.journal.noPeriods') }}</p>
        <div class="w-24">
          <NumberInput v-model="newYear" :min="2000" :max="2100" />
        </div>
        <button
          v-if="canEditPeriods"
          class="inline-flex items-center gap-1.5 rounded-md bg-brand-600 px-4 py-2 text-sm font-medium text-white hover:bg-brand-700 disabled:opacity-50"
          :disabled="createPeriodMutation.isPending.value"
          @click="handleCreatePeriod"
        >
          <Plus class="h-4 w-4" />
          {{ t('admin.accounting.journal.createPeriod') }}
        </button>
      </div>

      <div v-else class="flex flex-wrap items-center gap-3">
        <div class="flex items-center gap-2">
          <label class="text-sm font-medium text-gray-700">{{ t('admin.accounting.journal.period') }}:</label>
          <Select v-model="selectedPeriodId" :options="periodOptions" width="content" />
        </div>

        <span
          v-if="selectedPeriod"
          :class="[
            'inline-flex rounded-full px-2.5 py-0.5 text-xs font-medium',
            selectedPeriod.status === 'open' ? 'bg-green-100 text-green-800' : 'bg-gray-100 text-gray-800',
          ]"
        >
          {{ selectedPeriod.status === 'open' ? t('admin.accounting.periods.open') : t('admin.accounting.periods.closed') }}
        </span>
        <span
          v-if="isReference"
          class="inline-flex rounded-full bg-amber-100 px-2.5 py-0.5 text-xs font-medium text-amber-800"
          data-testid="reference-chip"
        >
          {{ t('admin.accounting.periods.reference') }}
        </span>

        <button
          v-if="canEditPeriods && selectedPeriod?.status === 'open'"
          class="inline-flex items-center gap-1.5 rounded-md border border-gray-300 px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50"
          :disabled="closePeriodMutation.isPending.value"
          :title="t('admin.accounting.journal.closePeriodTooltip')"
          @click="handleClosePeriod"
        >
          <Lock class="h-4 w-4" />
          {{ t('admin.accounting.journal.closePeriod') }}
        </button>
        <button
          v-if="canEditPeriods && selectedPeriod?.status === 'closed'"
          class="inline-flex items-center gap-1.5 rounded-md border border-gray-300 px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50"
          :disabled="reopenPeriodMutation.isPending.value"
          :title="t('admin.accounting.journal.reopenPeriodTooltip')"
          @click="handleReopenPeriod"
        >
          <Unlock class="h-4 w-4" />
          {{ t('admin.accounting.journal.reopenPeriod') }}
        </button>
        <button
          v-if="canBook && selectedPeriod"
          class="inline-flex min-h-11 items-center gap-1.5 rounded-md border border-amber-300 bg-white px-3 py-2 text-sm font-medium text-amber-800 hover:bg-amber-50 disabled:opacity-50"
          :disabled="!!selectedPeriod.reference_blocked || setReferenceMutation.isPending.value"
          data-testid="toggle-reference"
          @click="handleToggleReference"
        >
          {{ isReference ? t('admin.accounting.periods.clearReference') : t('admin.accounting.periods.markReference') }}
        </button>

        <div class="ml-auto flex items-center gap-2">
          <template v-if="canEditPeriods">
          <div v-if="showCreateYear" class="w-20">
            <NumberInput v-model="newYear" :min="2000" :max="2100" />
          </div>
          <button
            :class="[
              'inline-flex items-center gap-1.5 rounded-md px-3 py-2 text-sm font-medium disabled:opacity-50',
              showCreateYear
                ? 'bg-brand-600 text-white hover:bg-brand-700'
                : 'border border-gray-300 text-gray-700 hover:bg-gray-50',
            ]"
            :title="t('admin.accounting.journal.createPeriodTooltip')"
            :disabled="createPeriodMutation.isPending.value"
            @click="showCreateYear ? handleCreatePeriod() : (showCreateYear = true)"
          >
            <Plus class="h-4 w-4" />
            {{ t('admin.accounting.journal.createPeriod') }}
          </button>
          </template>
          <template v-if="canBook">
          <button
            v-if="!isReference"
            class="inline-flex items-center gap-1.5 rounded-md bg-brand-600 px-4 py-2 text-sm font-medium text-white hover:bg-brand-700 disabled:opacity-50"
            :disabled="syncing || !selectedPeriodId"
            :title="t('admin.accounting.journal.updatePeriodTooltip')"
            @click="handleSync"
          >
            <RefreshCw class="h-4 w-4" :class="{ 'animate-spin': syncing }" />
            {{ syncing ? t('admin.accounting.journal.updating') : t('admin.accounting.journal.updatePeriod') }}
          </button>
          <button
            v-if="!isReference"
            class="inline-flex items-center gap-1.5 rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50"
            :disabled="resplitting || !selectedPeriodId"
            :title="t('admin.accounting.journal.resplitTooltip')"
            data-testid="resplit-invoice-bilags"
            @click="handleResplitInvoiceBilags"
          >
            <RefreshCw class="h-4 w-4" :class="{ 'animate-spin': resplitting }" />
            {{ resplitting ? t('admin.accounting.journal.updating') : t('admin.accounting.journal.resplitInvoiceBilags') }}
          </button>
          <button
            v-if="!isReference"
            class="inline-flex items-center gap-1.5 rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50"
            :disabled="postingVippsDrafts || !selectedPeriodId"
            :title="t('admin.accounting.journal.vippsDraftsTooltip')"
            data-testid="post-vipps-drafts"
            @click="handlePostVippsDrafts"
          >
            <RefreshCw class="h-4 w-4" :class="{ 'animate-spin': postingVippsDrafts }" />
            {{ postingVippsDrafts ? t('admin.accounting.journal.updating') : t('admin.accounting.journal.vippsDrafts') }}
          </button>
          </template>
        </div>
      </div>

      <p
        v-if="selectedPeriod?.reference_blocked && canBook"
        class="mt-2 text-sm text-gray-600"
        data-testid="reference-blocked"
      >
        {{ t(`admin.accounting.periods.referenceBlocked.${selectedPeriod.reference_blocked}`) }}
      </p>
      <p
        v-if="isReference"
        class="mt-2 rounded-md bg-amber-50 px-3 py-2 text-sm text-amber-900"
        data-testid="reference-note"
      >
        {{ t('admin.accounting.referencePeriod') }}
      </p>
      <p
        v-if="voidRefusal"
        class="mt-2 rounded-md bg-amber-50 px-3 py-2 text-sm text-amber-900"
        role="alert"
        :data-testid="`void-refused-${voidRefusal}`"
      >
        {{ t(VOID_REFUSALS[voidRefusal].text) }}
        <RouterLink :to="VOID_REFUSALS[voidRefusal].to" class="inline-flex min-h-11 items-center font-semibold text-brand-700 underline">
          {{ t(VOID_REFUSALS[voidRefusal].link) }}
        </RouterLink>
      </p>
      <ClearedSettlementRefusalNote
        v-if="clearedRefusal"
        :refusal="clearedRefusal.refusal"
        @undone="voidAgain"
      />
      <p
        v-if="syncMessage"
        class="mt-2 text-sm"
        :class="syncMessage.startsWith(t('common.error')) ? 'text-red-600' : 'text-green-600'"
      >
        {{ syncMessage }}
      </p>
      <div v-if="syncSkipped.length" class="mt-2 text-sm text-amber-700">
        <p class="font-medium">{{ t('admin.accounting.journal.syncSkippedHeading') }}</p>
        <ul class="list-disc pl-5">
          <li v-for="s in syncSkipped" :key="s.ref">{{ s.ref }}: {{ s.reason }}</li>
        </ul>
      </div>
      <div v-if="canBook && vippsReleasable.length" class="mt-2 text-sm" data-testid="vipps-releasable">
        <p class="font-medium text-gray-700">{{ t('admin.accounting.journal.vippsDraftsReleasableHeading') }}</p>
        <ul class="mt-1 space-y-1">
          <li v-for="r in vippsReleasable" :key="r.row_id" class="flex flex-wrap items-center gap-2">
            <span>{{ r.ref }} · {{ r.date }} · {{ r.amount.toLocaleString('nb-NO') }} kr</span>
            <button
              class="rounded-md border border-gray-300 bg-white px-2 py-0.5 text-xs font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50"
              :disabled="releasingRowId !== ''"
              @click="handleReleaseVippsCharge(r)"
            >
              {{ t('admin.accounting.journal.vippsDraftsRelease') }}
            </button>
          </li>
        </ul>
      </div>

      <!-- Posted Vipps charges parked on 3900: preview, then apply with a fresh TOTP (BRY-557). -->
      <VippsFallbackReclass v-if="canBook" />

    </div>

    <!-- Action bar -->
    <div class="mt-4 flex flex-wrap items-center gap-3">
      <RouterLink
        v-if="canBook"
        to="/admin/accounting/journal/new"
        class="inline-flex items-center gap-1.5 rounded-md bg-brand-600 px-4 py-2 text-sm font-medium text-white hover:bg-brand-700"
      >
        <Plus class="h-4 w-4" />
        {{ t('admin.accounting.journal.newEntry') }}
      </RouterLink>

      <div class="relative">
        <Search class="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" />
        <input
          v-model="searchQ"
          type="search"
          class="rounded-md border border-gray-300 py-2 pl-8 pr-3 text-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
          :placeholder="t('common.search')"
        />
      </div>

      <div class="flex items-center gap-2">
        <label class="text-sm font-medium text-gray-700">{{ t('admin.accounting.journal.status') }}:</label>
        <Select v-model="statusFilter" :options="statusFilterOptions" width="content" />
      </div>

      <div class="flex items-center gap-2">
        <label class="text-sm font-medium text-gray-700">{{ t('admin.accounting.journal.source') }}:</label>
        <Select v-model="sourceFilter" :options="sourceFilterOptions" width="content" />
      </div>
    </div>

    <!-- Table -->
    <div v-if="isLoading" class="mt-6 text-gray-500">{{ t('common.loading') }}...</div>

    <div v-else class="mt-6 overflow-x-auto">
      <table class="min-w-full divide-y divide-gray-200">
        <thead class="bg-gray-50">
          <tr>
            <th class="w-8 px-2 py-3"></th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              <button class="inline-flex items-center gap-1 hover:text-gray-800" :title="t('admin.accounting.journal.tooltipEntryNumber')" @click="toggleSort('entry_number')">
                {{ t('admin.accounting.journal.entryNumber') }}
                <ChevronUp v-if="sortBy === 'entry_number' && sortDir === 'asc'" class="h-3 w-3" />
                <ChevronDown v-else-if="sortBy === 'entry_number'" class="h-3 w-3" />
              </button>
            </th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              <button class="inline-flex items-center gap-1 hover:text-gray-800" :title="t('admin.accounting.journal.tooltipDate')" @click="toggleSort('entry_date')">
                {{ t('admin.accounting.journal.date') }}
                <ChevronUp v-if="sortBy === 'entry_date' && sortDir === 'asc'" class="h-3 w-3" />
                <ChevronDown v-else-if="sortBy === 'entry_date'" class="h-3 w-3" />
              </button>
            </th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              <button class="inline-flex items-center gap-1 hover:text-gray-800" :title="t('admin.accounting.journal.tooltipDescription')" @click="toggleSort('description')">
                {{ t('admin.accounting.journal.description') }}
                <ChevronUp v-if="sortBy === 'description' && sortDir === 'asc'" class="h-3 w-3" />
                <ChevronDown v-else-if="sortBy === 'description'" class="h-3 w-3" />
              </button>
            </th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              <span class="inline-flex items-center gap-1" :title="t('admin.accounting.journal.tooltipAccounts')">{{ t('admin.accounting.accounts.code') }}</span>
            </th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              <button class="inline-flex items-center gap-1 hover:text-gray-800" :title="t('admin.accounting.journal.tooltipStatus')" @click="toggleSort('status')">
                {{ t('admin.accounting.journal.status') }}
                <ChevronUp v-if="sortBy === 'status' && sortDir === 'asc'" class="h-3 w-3" />
                <ChevronDown v-else-if="sortBy === 'status'" class="h-3 w-3" />
              </button>
            </th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              <button class="inline-flex items-center gap-1 hover:text-gray-800" :title="t('admin.accounting.journal.tooltipSource')" @click="toggleSort('source')">
                {{ t('admin.accounting.journal.source') }}
                <ChevronUp v-if="sortBy === 'source' && sortDir === 'asc'" class="h-3 w-3" />
                <ChevronDown v-else-if="sortBy === 'source'" class="h-3 w-3" />
              </button>
            </th>
            <th class="w-8 px-2 py-3"></th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.accounting.journal.actions') }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 bg-white">
          <template v-for="entry in entries" :key="entry.id">
            <tr class="cursor-pointer hover:bg-gray-50" @click="toggleExpand(entry)">
              <td class="px-2 py-3 text-gray-400">
                <component :is="expandedId === entry.id ? ChevronDown : ChevronRight" class="h-4 w-4" />
              </td>
              <td class="whitespace-nowrap px-4 py-3 text-sm font-mono text-gray-900">{{ entry.entry_number }}</td>
              <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ entry.entry_date }}</td>
              <td class="px-4 py-3 text-sm text-gray-900">{{ entry.description }}</td>
              <td class="px-4 py-3">
                <div v-if="entry.lines?.length" class="flex flex-wrap gap-1">
                  <span
                    v-for="line in entry.lines"
                    :key="line.id"
                    :class="['inline-flex rounded px-1.5 py-0.5 font-mono text-xs font-semibold', accountTypeColors[line.account_type] ?? 'bg-gray-100 text-gray-800']"
                  >
                    {{ line.account_code }}
                  </span>
                </div>
                <span v-else class="text-sm text-gray-400">-</span>
              </td>
              <td class="whitespace-nowrap px-4 py-3">
                <span
                  :class="[
                    'inline-flex rounded-full px-2.5 py-0.5 text-xs font-medium',
                    statusColors[entry.status] ?? 'bg-gray-100 text-gray-800',
                  ]"
                >
                  {{ statusLabels[entry.status] ?? entry.status }}
                </span>
              </td>
              <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">
                {{ sourceLabels[entry.source] ?? entry.source }}
              </td>
              <td class="px-2 py-3" @click.stop>
                <button
                  v-if="entry.attachment_url"
                  class="text-gray-400 hover:text-brand-600"
                  :title="t('admin.accounting.journal.viewBilag')"
                  @click="openAttachment(entry.id)"
                >
                  <Paperclip class="h-4 w-4" />
                </button>
              </td>
              <td class="whitespace-nowrap px-4 py-3" @click.stop>
                <div v-if="canBook" class="flex items-center gap-2">
                  <button
                    v-if="entry.status === 'draft'"
                    class="text-green-600 hover:text-green-800 disabled:opacity-50"
                    :disabled="postMutation.isPending.value"
                    :title="t('admin.accounting.journal.post')"
                    @click="handlePost(entry.id)"
                  >
                    <Send class="h-4 w-4" />
                  </button>
                  <button
                    v-if="entry.status === 'draft'"
                    class="text-gray-500 hover:text-red-700 disabled:opacity-50"
                    :disabled="deleteDraftMutation.isPending.value"
                    :title="t('admin.accounting.journal.deleteDraft')"
                    @click="handleDeleteDraft(entry.id)"
                  >
                    <Trash2 class="h-4 w-4" />
                  </button>
                  <button
                    v-if="entry.status === 'posted'"
                    class="text-red-600 hover:text-red-800 disabled:opacity-50"
                    :disabled="voidMutation.isPending.value"
                    :title="t('admin.accounting.journal.void')"
                    :data-testid="`void-entry-${entry.id}`"
                    @click="handleVoid(entry.id)"
                  >
                    <Ban class="h-4 w-4" />
                  </button>
                </div>
              </td>
            </tr>
            <tr v-if="expandedId === entry.id">
              <td colspan="9" class="bg-gray-50 px-8 py-4">
                <div v-if="entry.lines?.length">
                  <table class="min-w-full text-sm">
                    <thead>
                      <tr class="text-xs font-medium uppercase text-gray-500">
                        <th class="pb-2 pr-4 text-left">{{ t('admin.accounting.accounts.code') }}</th>
                        <th class="pb-2 pr-4 text-left">{{ t('admin.accounting.accounts.name') }}</th>
                        <th class="pb-2 pr-4 text-right">{{ t('admin.accounting.journalForm.debit') }}</th>
                        <th class="pb-2 pr-4 text-right">{{ t('admin.accounting.journalForm.credit') }}</th>
                        <th class="pb-2 pr-4 text-right">{{ t('admin.accounting.journalForm.mva') }}</th>
                        <th class="pb-2 text-left">{{ t('admin.accounting.journal.description') }}</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr v-for="line in entry.lines" :key="line.id" class="border-t border-gray-200">
                        <td class="py-2 pr-4">
                          <span :class="['inline-flex rounded px-1.5 py-0.5 font-mono text-sm font-semibold', accountTypeColors[line.account_type] ?? 'bg-gray-100 text-gray-800']">
                            {{ line.account_code }}
                          </span>
                        </td>
                        <td class="py-2 pr-4">{{ line.account_name }}</td>
                        <td class="py-2 pr-4 text-right">{{ formatNOK(line.debit) }}</td>
                        <td class="py-2 pr-4 text-right">{{ formatNOK(line.credit) }}</td>
                        <td class="py-2 pr-4 text-right">{{ formatNOK(line.mva_amount) }}</td>
                        <td class="py-2">{{ line.description }}</td>
                      </tr>
                    </tbody>
                  </table>
                </div>
                <p v-else class="text-sm text-gray-500">{{ t('admin.accounting.journal.noEntries') }}</p>
              </td>
            </tr>
          </template>
        </tbody>
      </table>
      <p v-if="!entries?.length" class="mt-4 text-center text-sm text-gray-500">
        {{ t('admin.accounting.journal.noEntries') }}
      </p>
    </div>
  </div>
</template>
