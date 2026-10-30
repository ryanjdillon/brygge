<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { FileDown, Send, Trash2, Ban, Bell, RefreshCw, KeyRound, Copy, Check, ChevronUp, ChevronDown, X, Link2, Scale, Undo2 } from 'lucide-vue-next'
import FakturaArchiveButton from '@/components/admin/FakturaArchiveButton.vue'
import DeliveryLogButton from '@/components/admin/DeliveryLogButton.vue'
import CreditNoteSearchModal, { type CreditSearchHit } from '@/components/admin/CreditNoteSearchModal.vue'
import InvoiceNumberChip from '@/components/ui/InvoiceNumberChip.vue'
import RefundOrderButton from '@/components/admin/RefundOrderButton.vue'
import ReissueAfterRefundButton from '@/components/admin/ReissueAfterRefundButton.vue'
import RefundStateChip from '@/components/admin/RefundStateChip.vue'
import { disputeChipKind, refundChipKind } from '@/lib/refundState'
import type { ReissuedInvoice } from '@/composables/useReissueAfterRefund'
import { useConfirm, useConfirmStore } from '@/stores/confirm'
import { useFreshTotp } from '@/composables/useFreshTotp'
import { copyPayLink as copyPayLinkFor, payLinkErrorKey } from '@/composables/useCopyPayLink'
import { useRangeSelect } from '@/composables/useRangeSelect'
import { useCan } from '@/composables/useCan'
import { formatNOK, formatDate, formatDateTime } from '@/lib/format'

interface Row {
  id: string
  invoice_number: number
  user_id: string | null
  member_name: string
  member_email: string
  member_phone?: string
  total_amount: number
  issue_date: string
  due_date: string
  price_item_name: string
  fiscal_year: number | null
  description: string
  created_at: string
  sent_at: string | null
  last_reminder_at: string | null
  status: string
  paid: boolean
  online_paid_at: string | null
  online_payment_opt_in: boolean | null
  // Credit-note links (BRY-342): set on a credit note linked to the faktura
  // it credits, and on a faktura settled by one.
  credits_invoice_number: number | null
  // 'refund': a credit note a card refund issued, linked to its faktura
  // (credits_invoice_number; none for an order with no faktura) through
  // the refund. It is never linked, unlinked or voided by hand.
  // 'refunded': a faktura that card refunds credited in full.
  credit_state: 'settled_pair' | 'credit_owed' | 'unlinked' | 'refund' | 'refunded' | null
  credited_by_invoice_number: number | null
  // A document a card refund issued (its credit note, or the faktura after
  // a failed refund): corrected by a new document, never voided.
  refund_document?: boolean
  // The order to open the refund dialog for: set by the server on a
  // card-paid faktura the treasurer or admin can refund.
  refund_order_id?: string | null
  // What card refunds did to it (none | partial | full), from the credit
  // notes they issued, and those notes.
  refund_state?: string
  refunded_amount?: number
  refund_credit_notes?: { id: string; number: number; amount: number; has_pdf: boolean }[]
  // "Ny faktura" after a full refund: possible for a treasurer or admin
  // until one is issued; then the number of the new faktura, and on that
  // one the number it replaces.
  can_reissue?: boolean
  reissued_invoice_number?: number | null
  reissue_of_invoice_number?: number | null
  // The latest dispute against the card payment, and Stripe Dashboard
  // pages (treasurer and admin only).
  dispute_status?: string | null
  dispute_due_by?: string | null
  dispute_url?: string
  payment_url?: string
}

type PaidStatus = 'paid' | 'waiting' | 'past_due' | 'credit'

const props = defineProps<{
  /** Backend filter — drives which rows are listed and which actions are shown. */
  status: 'draft' | 'sent' | 'voided' | 'imported'
}>()

const { t } = useI18n()
const confirm = useConfirm()
const confirmStore = useConfirmStore()
const { ensureFreshTotp, totpAwareFetch } = useFreshTotp()

const rows = ref<Row[]>([])
const loading = ref(true)
const error = ref<string | null>(null)
const notice = ref<string | null>(null)
let noticeTimer: ReturnType<typeof setTimeout> | undefined
function flash(message: string) {
  notice.value = message
  if (noticeTimer) clearTimeout(noticeTimer)
  noticeTimer = setTimeout(() => (notice.value = null), 5000)
}
const busyIds = ref<Set<string>>(new Set())
const selected = ref<Set<string>>(new Set())
const filterMember = ref('')
const filterPriceItem = ref('')
const filterYear = ref<number | ''>('')
const paidStatusFilter = ref<Set<PaidStatus>>(new Set())

// Seed the paid-status chips from the `pay` query param so the Økonomi
// summary cards can deep-link into the sent list pre-filtered (e.g.
// `?tab=sent&pay=waiting,past_due` for outstanding). Only the sent list
// carries these chips; drafts/voided ignore a stray param.
const route = useRoute()
const VALID_PAID_STATUS: PaidStatus[] = ['paid', 'waiting', 'past_due', 'credit']
// The sent and imported lists carry paid-status chips; imported history
// holds most credit notes.
const showPaidChips = computed(() => props.status === 'sent' || props.status === 'imported')
function applyPayQuery() {
  if (!showPaidChips.value) {
    paidStatusFilter.value = new Set()
    return
  }
  const raw = route.query.pay
  const tokens = (typeof raw === 'string' ? raw.split(',') : []).filter(
    (tok): tok is PaidStatus => (VALID_PAID_STATUS as string[]).includes(tok),
  )
  paidStatusFilter.value = new Set(tokens)
}
applyPayQuery()
watch(() => route.query.pay, applyPayQuery)

// A credit note is never a debt, and a faktura settled by one is settled
// like a paid one — neither may show as past due (BRY-342).
function isCreditNote(d: Row): boolean {
  return Number(d.total_amount) < 0
}
function rowPaidStatus(d: Row): PaidStatus {
  if (isCreditNote(d)) return 'credit'
  if (d.paid || d.credited_by_invoice_number != null) return 'paid'
  return new Date(d.due_date) < new Date(new Date().toDateString()) ? 'past_due' : 'waiting'
}
function paidStatusTitle(d: Row): string {
  const labels: Record<PaidStatus, string> = {
    paid: t('admin.faktura.sent.filterPaid'),
    waiting: t('admin.faktura.sent.filterWaiting'),
    past_due: t('admin.faktura.sent.filterPastDue'),
    credit: t('admin.faktura.credit.filter'),
  }
  return labels[rowPaidStatus(d)]
}
function togglePaidStatus(s: PaidStatus) {
  const next = new Set(paidStatusFilter.value)
  if (next.has(s)) next.delete(s)
  else next.add(s)
  paidStatusFilter.value = next
}

// Per-status counts across the full row set (independent of the
// paid-status chip filter itself) so the chips show "what would my
// filter become if I toggled this on" and the operator can answer
// "how many past-due fakturas are there" at a glance.
const paidStatusCounts = computed(() => {
  const acc = { paid: 0, waiting: 0, past_due: 0, credit: 0 } as Record<PaidStatus, number>
  for (const d of rows.value) acc[rowPaidStatus(d)]++
  return acc
})

async function load() {
  loading.value = true
  error.value = null
  selected.value = new Set()
  try {
    const res = await fetch(`/api/v1/admin/financials/invoices?status=${props.status}`, { credentials: 'include' })
    if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
    const body = await res.json()
    rows.value = body.items ?? []
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    loading.value = false
  }
}

onMounted(load)
watch(() => props.status, load)

const filtered = computed(() => rows.value.filter((d) => {
  if (filterMember.value) {
    const q = filterMember.value.toLowerCase()
    if (!d.member_name.toLowerCase().includes(q) && !d.member_email.toLowerCase().includes(q)) return false
  }
  if (filterPriceItem.value && d.price_item_name !== filterPriceItem.value) return false
  if (filterYear.value !== '' && d.fiscal_year !== filterYear.value) return false
  if (paidStatusFilter.value.size > 0 && !paidStatusFilter.value.has(rowPaidStatus(d))) return false
  return true
}))

// Click-to-sort: first click → asc, second → desc, third → off (revert
// to backend ORDER BY created_at DESC). Nulls always sort last.
type SortKey = 'invoice_number' | 'member_name' | 'price_item_name' | 'fiscal_year'
  | 'total_amount' | 'due_date' | 'sent_at' | 'last_reminder_at'
const sortKey = ref<SortKey | null>(null)
const sortDir = ref<'asc' | 'desc'>('asc')

function setSort(key: SortKey) {
  if (sortKey.value !== key) {
    sortKey.value = key
    sortDir.value = 'asc'
  } else if (sortDir.value === 'asc') {
    sortDir.value = 'desc'
  } else {
    sortKey.value = null
  }
}

function sortVal(d: Row, key: SortKey): string | number | null {
  switch (key) {
    case 'invoice_number': return d.invoice_number
    case 'member_name': return d.member_name.toLowerCase()
    case 'price_item_name': return (d.price_item_name || d.description || '').toLowerCase()
    case 'fiscal_year': return d.fiscal_year
    case 'total_amount': return Number(d.total_amount)
    case 'due_date': return d.due_date
    case 'sent_at': return d.sent_at
    case 'last_reminder_at': return d.last_reminder_at
  }
}

const sorted = computed<Row[]>(() => {
  if (sortKey.value === null) return filtered.value
  const key = sortKey.value
  const dir = sortDir.value === 'asc' ? 1 : -1
  return [...filtered.value].sort((a, b) => {
    const va = sortVal(a, key)
    const vb = sortVal(b, key)
    // Null/undefined always last regardless of direction.
    if (va == null && vb == null) return 0
    if (va == null) return 1
    if (vb == null) return -1
    if (va < vb) return -1 * dir
    if (va > vb) return 1 * dir
    return 0
  })
})

const priceItemOptions = computed(() => {
  const set = new Set<string>()
  for (const d of rows.value) if (d.price_item_name) set.add(d.price_item_name)
  return [...set].sort()
})
const yearOptions = computed(() => {
  const set = new Set<number>()
  for (const d of rows.value) if (d.fiscal_year) set.add(d.fiscal_year)
  return [...set].sort((a, b) => b - a)
})

const totalAmount = computed(() => filtered.value.reduce((s, d) => s + Number(d.total_amount), 0))
const allFilteredSelected = computed(() =>
  filtered.value.length > 0 && filtered.value.every((d) => selected.value.has(d.id)),
)

// Board members without the money role see the list but none of its
// actions; the server would refuse them anyway (BRY-372).
const { can } = useCan()
const canAct = can('money')
const showSend = computed(() => canAct && props.status === 'draft')
const showResend = computed(() => canAct && props.status === 'sent')
const showPayLink = computed(() => canAct && props.status === 'sent')
// Imported historicals are migrated accounting records — read-only here (no
// send/delete/void). Resolving them as paid happens via bank reconciliation
// (Tildel), not this list. See BRY-239.
const showDelete = computed(() => canAct && props.status !== 'sent' && props.status !== 'imported') // backend rejects DELETE on sent open invoices
const showVoid = computed(() => canAct && props.status !== 'voided' && props.status !== 'imported')
const showReminder = computed(() => canAct && props.status === 'sent')
const showRegenerate = computed(() => canAct && props.status === 'sent')
const showReissueKid = computed(() => canAct && props.status === 'sent')

const { onCheckboxClick: onRowClick, resetAnchor } = useRangeSelect(selected, () => sorted.value)
function toggleAll() {
  const next = new Set(selected.value)
  if (allFilteredSelected.value) for (const d of filtered.value) next.delete(d.id)
  else for (const d of filtered.value) next.add(d.id)
  selected.value = next
  resetAnchor()
}
function clearSelection() {
  selected.value = new Set()
  resetAnchor()
}

function rowLabel(d: Row): string {
  return `#${d.invoice_number} — ${d.member_name} (${formatNOK(Number(d.total_amount))})`
}

// Send and void have already happened when the GL booking is attempted, so a
// booking failure comes back in the response instead of as an error. Show it
// so the treasurer can run the repair sync in Bilag.
function reportGLFailure(body: { gl_booking?: { status: string; error?: string } } | null) {
  if (body?.gl_booking?.status === 'failed') {
    error.value = t('admin.invoiceDrafts.glBookingFailed', { reason: body.gl_booking.error ?? '' })
  }
}

async function doSend(id: string) {
  busyIds.value.add(id)
  try {
    const res = await totpAwareFetch(`/api/v1/admin/financials/invoices/${id}/send`, { method: 'POST' })
    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      error.value = `${res.status} ${txt}`
      return
    }
    reportGLFailure(await res.json().catch(() => null))
    rows.value = rows.value.filter((d) => d.id !== id)
    selected.value.delete(id)
  } finally {
    busyIds.value.delete(id)
  }
}

async function doResend(id: string) {
  // Unlike doSend, the row STAYS in the sent list — the original
  // sent_at is preserved server-side and the resend is recorded
  // only via the audit log (action=invoice.emailed, resend=true).
  busyIds.value.add(id)
  try {
    const res = await totpAwareFetch(`/api/v1/admin/financials/invoices/${id}/resend`, { method: 'POST' })
    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      error.value = `${res.status} ${txt}`
      return
    }
    const to = rows.value.find((d) => d.id === id)?.member_email ?? ''
    flash(t('admin.faktura.sent.resendSuccess', { email: to }))
  } finally {
    busyIds.value.delete(id)
  }
}

// Fetches the invoice's durable /betal pay link and copies it to the clipboard
// so the treasurer can send it however they like (SMS, chat) — handy for members
// who don't cope well with email / magic links. No fresh-TOTP gate: it's a
// benign read that only ensures the checkout order exists.
async function copyPayLink(d: Row) {
  if (busyIds.value.has(d.id)) return
  busyIds.value.add(d.id)
  try {
    const r = await copyPayLinkFor(`/api/v1/admin/financials/invoices/${d.id}/pay-link`)
    flash(r.ok ? t('admin.payLink.copied') : t(payLinkErrorKey(r.reason)))
  } finally {
    busyIds.value.delete(d.id)
  }
}

// Per-invoice online-payment override (BRY-283): Auto (null) / On (true) / Off (false).
function optInToStr(v: boolean | null): 'auto' | 'on' | 'off' {
  return v === true ? 'on' : v === false ? 'off' : 'auto'
}
async function setOnlinePayment(d: Row, value: string) {
  if (busyIds.value.has(d.id)) return
  const optIn = value === 'on' ? true : value === 'off' ? false : null
  busyIds.value.add(d.id)
  try {
    const res = await fetch(`/api/v1/admin/financials/invoices/${d.id}/online-payment`, {
      method: 'PATCH',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ opt_in: optIn }),
    })
    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      error.value = `${res.status} ${txt}`
      return
    }
    d.online_payment_opt_in = optIn
  } finally {
    busyIds.value.delete(d.id)
  }
}

async function doDelete(id: string) {
  busyIds.value.add(id)
  try {
    const res = await totpAwareFetch(`/api/v1/admin/financials/invoices/${id}`, { method: 'DELETE' })
    if (res.status === 409) {
      const body = await res.json().catch(() => null)
      error.value = body?.code === 'imported' ? t('admin.faktura.importedReadOnly') : (body?.error ?? '409')
      return
    }
    if (!res.ok && res.status !== 204) {
      const txt = await res.text().catch(() => '')
      error.value = `${res.status} ${txt}`
      return
    }
    rows.value = rows.value.filter((d) => d.id !== id)
    selected.value.delete(id)
  } finally {
    busyIds.value.delete(id)
  }
}

async function doVoid(id: string) {
  busyIds.value.add(id)
  try {
    const res = await totpAwareFetch(`/api/v1/admin/financials/invoices/${id}/void`, { method: 'POST' })
    if (res.status === 409) {
      // Payment already received (reconciled or via Vipps), or the faktura
      // is booked in a closed year: either way it needs a credit note, not
      // a void.
      const body = await res.json().catch(() => null)
      error.value =
        body?.code === 'imported'
          ? t('admin.faktura.importedReadOnly')
          : body?.code === 'period_closed'
          ? t('admin.invoiceDrafts.voidPeriodClosed')
          : body?.code === 'credit_linked'
            ? t('admin.faktura.credit.voidLinked')
            : body?.code === 'credited'
              ? t('admin.faktura.credit.voidCredited', { number: body.credit_note_number })
              : t('admin.invoiceDrafts.voidSettled')
      return
    }
    if (!res.ok && res.status !== 204) {
      const txt = await res.text().catch(() => '')
      error.value = `${res.status} ${txt}`
      return
    }
    if (res.status === 200) reportGLFailure(await res.json().catch(() => null))
    rows.value = rows.value.filter((d) => d.id !== id)
    selected.value.delete(id)
  } finally {
    busyIds.value.delete(id)
  }
}

// The choice value that opens the manual search instead of linking.
const SEARCH_OTHER = '__search__'

// Manual faktura search for a credit note (BRY-393). The Tildel picker in
// BankReconcileTab is inline and bound to bank/Vipps rows (payer alias,
// row-specific fetchers, assign), so this reuses its Modal and list layout
// rather than the picker itself.
const creditSearch = ref<{ note: Row; notice: string | null; error: string | null } | null>(null)
const creditSearchOpen = computed({
  get: () => creditSearch.value != null,
  set: (open: boolean) => {
    if (!open) creditSearch.value = null
  },
})

// Settle a credit note against the faktura it credits. Linking changes what
// the member owes, so the server requires a fresh step-up — prompt for it
// at click time, before the picker.
async function settleCredit(d: Row) {
  if (!(await ensureFreshTotp())) return
  busyIds.value.add(d.id)
  try {
    const res = await totpAwareFetch(`/api/v1/admin/financials/invoices/${d.id}/credit-candidates`)
    if (!res.ok) {
      error.value = `${res.status} ${await res.text().catch(() => '')}`
      return
    }
    const candidates: CreditSearchHit[] = (await res.json()).items ?? []
    if (candidates.length === 0) {
      creditSearch.value = {
        note: d,
        notice: t('admin.faktura.credit.noCandidates', { number: d.invoice_number }),
        error: null,
      }
      return
    }
    const ok = await confirm({
      title: t('admin.faktura.credit.settleTitle', { number: d.invoice_number }),
      body: t('admin.faktura.credit.settleBody', { amount: formatNOK(Math.abs(Number(d.total_amount))) }),
      choice: {
        label: t('admin.faktura.credit.settleChoice'),
        options: [
          ...candidates.map((c) => ({
            value: c.id,
            label: t(c.settled ? 'admin.faktura.credit.candidatePaid' : 'admin.faktura.credit.candidateOpen', {
              number: c.invoice_number,
              amount: formatNOK(Number(c.total_amount)),
              date: c.issue_date.slice(0, 10),
            }),
          })),
          { value: SEARCH_OTHER, label: t('admin.faktura.credit.searchOption') },
        ],
        default: candidates[0].id,
      },
      confirmLabel: t('admin.faktura.credit.settleAction'),
      tone: 'warning',
    })
    if (!ok) return
    const target = confirmStore.choiceValue
    if (target === SEARCH_OTHER) {
      creditSearch.value = { note: d, notice: null, error: null }
      return
    }
    error.value = await linkCredit(d, { invoice_id: target })
  } finally {
    busyIds.value.delete(d.id)
  }
}

// A faktura picked in the manual search. Across payers the server needs an
// explicit confirmation, unless the credit note has no payer at all.
async function pickCredit(hit: CreditSearchHit, creditNoteHasPayer: boolean) {
  const note = creditSearch.value?.note
  if (!note) return
  const otherPayer = creditNoteHasPayer && !hit.same_payer
  if (otherPayer) {
    const ok = await confirm({
      title: t('admin.faktura.credit.differentPayerTitle'),
      body: t('admin.faktura.credit.differentPayerBody', {
        credit: note.invoice_number,
        number: hit.invoice_number,
        payer: hit.payer_name || '—',
      }),
      confirmLabel: t('admin.faktura.credit.differentPayerAction'),
      tone: 'warning',
    })
    if (!ok) return
  }
  busyIds.value.add(note.id)
  if (creditSearch.value) creditSearch.value.error = null
  try {
    const failed = await linkCredit(note, {
      invoice_id: hit.id,
      manual_pick: true,
      ...(otherPayer ? { confirm_other_member: true } : {}),
    })
    // Keep the search open on a refusal so the reason shows next to the list.
    if (failed && creditSearch.value) creditSearch.value.error = failed
    else if (!failed) creditSearch.value = null
  } finally {
    busyIds.value.delete(note.id)
  }
}

// Links the credit note; returns the refusal message, or null on success.
async function linkCredit(d: Row, payload: Record<string, unknown>): Promise<string | null> {
  const link = await totpAwareFetch(`/api/v1/admin/financials/invoices/${d.id}/credit-link`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
  const body = await link.json().catch(() => null)
  if (!link.ok) {
    return body?.code
      ? t(`admin.faktura.credit.error.${body.code}`, body?.error ?? '')
      : `${link.status} ${body?.error ?? ''}`
  }
  flash(
    t(body?.outcome === 'credit_owed' ? 'admin.faktura.credit.doneOwed' : 'admin.faktura.credit.donePair', {
      credit: body?.credit_note_number,
      number: body?.invoice_number,
    }),
  )
  await load()
  return null
}

async function unlinkCredit(d: Row) {
  if (!(await ensureFreshTotp())) return
  const ok = await confirm({
    title: t('admin.faktura.credit.unlinkTitle', { number: d.invoice_number }),
    body: t('admin.faktura.credit.unlinkBody', { number: d.credits_invoice_number }),
    confirmLabel: t('admin.faktura.credit.unlinkAction'),
    tone: 'warning',
  })
  if (!ok) return
  busyIds.value.add(d.id)
  try {
    const res = await totpAwareFetch(`/api/v1/admin/financials/invoices/${d.id}/credit-link`, { method: 'DELETE' })
    if (!res.ok && res.status !== 204) {
      error.value = `${res.status} ${await res.text().catch(() => '')}`
      return
    }
    await load()
  } finally {
    busyIds.value.delete(d.id)
  }
}

async function sendOne(d: Row) {
  if (!(await ensureFreshTotp())) return
  const ok = await confirm({
    title: t('admin.invoiceDrafts.sendConfirmTitle'),
    body: t('admin.invoiceDrafts.sendOneBody', { name: d.member_name }),
    confirmLabel: t('admin.invoiceDrafts.sendAction'),
    tone: 'info',
  })
  if (!ok) return
  await doSend(d.id)
}

async function resendOne(d: Row) {
  if (!(await ensureFreshTotp())) return
  const ok = await confirm({
    title: t('admin.faktura.sent.resendConfirmTitle'),
    body: t('admin.faktura.sent.resendBody', {
      name: d.member_name,
      email: d.member_email || '',
    }),
    confirmLabel: t('admin.faktura.sent.resendAction'),
    tone: 'info',
  })
  if (!ok) return
  await doResend(d.id)
}
async function deleteOne(d: Row) {
  if (!(await ensureFreshTotp())) return
  const ok = await confirm({
    title: t('admin.invoiceDrafts.deleteConfirmTitle'),
    body: t('admin.invoiceDrafts.deleteOneBody', { name: d.member_name }),
    confirmLabel: t('common.delete'),
    tone: 'danger',
  })
  if (!ok) return
  await doDelete(d.id)
}
// The badge label of a credit note's state; a refund's credit note for an
// order with no faktura has no number to name.
function creditStateKey(d: Row): string {
  if (d.credit_state === 'refund' && d.credits_invoice_number == null) return 'refund_order'
  return d.credit_state ?? ''
}

// "Ny faktura" after a refund went through: say what became of it, since a
// faktura with nobody to email it to waits among the drafts.
function onReissued(res: ReissuedInvoice) {
  // Which step failed decides the words: no PDF, or no club details to make one from.
  const key = res.warnings?.includes('pdf_unavailable')
    ? 'doneWarning'
    : res.warnings?.length
      ? 'doneNoClub'
      : res.sent_at
        ? 'done'
        : 'doneUnsent'
  flash(t(`admin.refund.reissue.${key}`, { number: res.invoice_number }))
  load()
}
function onReissueFailed(message: string) {
  error.value = message
  load()
}

async function voidOne(d: Row) {
  if (!(await ensureFreshTotp())) return
  const ok = await confirm({
    title: t('admin.invoiceDrafts.voidConfirmTitle'),
    body: t('admin.invoiceDrafts.voidOneBody', { name: d.member_name }),
    confirmLabel: t('admin.invoiceDrafts.voidAction'),
    tone: 'warning',
  })
  if (!ok) return
  await doVoid(d.id)
}

// Copies the deduplicated email addresses of every selected (or, if
// nothing's selected, every filtered) faktura recipient to the
// clipboard. Use case: deliverability fallback — Gmail/Outlook
// sometimes route the automated faktura email to spam, so the
// treasurer wants to send a personal heads-up from their own
// account ("you just got a purring, check spam if you can't see it"
// kind of thing). Comma separator works for Gmail; semicolon users
// can replace it themselves.
const copyEmailsState = ref<{ count: number; total: number } | null>(null)
let copyEmailsResetTimer: ReturnType<typeof setTimeout> | null = null

async function copyEmailsSelected() {
  const source = selected.value.size > 0
    ? rows.value.filter((d) => selected.value.has(d.id))
    : filtered.value
  const seen = new Set<string>()
  const emails: string[] = []
  for (const d of source) {
    const e = (d.member_email || '').trim()
    if (e && !seen.has(e.toLowerCase())) {
      seen.add(e.toLowerCase())
      emails.push(e)
    }
  }
  if (emails.length === 0) {
    error.value = t('admin.faktura.copyEmails.noneFound')
    return
  }
  try {
    await navigator.clipboard.writeText(emails.join(', '))
    copyEmailsState.value = { count: emails.length, total: source.length }
    if (copyEmailsResetTimer) clearTimeout(copyEmailsResetTimer)
    copyEmailsResetTimer = setTimeout(() => {
      copyEmailsState.value = null
    }, 4000)
  } catch (e: any) {
    error.value = e?.message ?? 'Clipboard write failed'
  }
}

async function sendSelected() {
  if (selected.value.size === 0) return
  if (!(await ensureFreshTotp())) return
  const items = rows.value.filter((d) => selected.value.has(d.id))
  const ok = await confirm({
    title: t('admin.invoiceDrafts.sendConfirmTitle'),
    body: t('admin.invoiceDrafts.sendBulkBody', { n: items.length }),
    details: items.map(rowLabel),
    confirmLabel: t('admin.invoiceDrafts.sendAction'),
    tone: 'info',
  })
  if (!ok) return
  for (const id of items.map((d) => d.id)) await doSend(id)
}

async function deleteSelected() {
  if (selected.value.size === 0) return
  if (!(await ensureFreshTotp())) return
  const items = rows.value.filter((d) => selected.value.has(d.id))
  const ok = await confirm({
    title: t('admin.invoiceDrafts.deleteConfirmTitle'),
    body: t('admin.invoiceDrafts.deleteBulkBody', { n: items.length }),
    details: items.map(rowLabel),
    confirmLabel: t('common.delete'),
    tone: 'danger',
  })
  if (!ok) return
  for (const id of items.map((d) => d.id)) await doDelete(id)
}

async function sendRemindersSelected() {
  if (selected.value.size === 0) return
  if (!(await ensureFreshTotp())) return
  const items = rows.value.filter((d) => selected.value.has(d.id) && !d.paid && !isCreditNote(d))
  if (items.length === 0) {
    error.value = t('admin.faktura.sent.reminderNoneEligible')
    return
  }
  const ok = await confirm({
    title: t('admin.faktura.sent.reminderConfirmTitle'),
    body: t('admin.faktura.sent.reminderBulkBody', { n: items.length }),
    details: items.map(rowLabel),
    confirmLabel: t('admin.faktura.sent.reminderAction'),
    tone: 'info',
    // Online-payment override for this batch (BRY-283): auto = leave each invoice's
    // setting (Auto still follows the club cap); on/off persist to the reminded
    // invoices. Default auto, so reminders never clobber per-row overrides.
    choice: {
      label: t('admin.faktura.sent.reminderOnlinePaymentChoice.label'),
      default: 'auto',
      options: [
        { value: 'auto', label: t('admin.faktura.sent.reminderOnlinePaymentChoice.auto') },
        { value: 'on', label: t('admin.faktura.sent.reminderOnlinePaymentChoice.on') },
        { value: 'off', label: t('admin.faktura.sent.reminderOnlinePaymentChoice.off') },
      ],
    },
  })
  if (!ok) return
  const choice = confirmStore.choiceValue
  const ids = items.map((d) => d.id)
  const body: { ids: string[]; online_payment?: boolean } = { ids }
  if (choice === 'on') body.online_payment = true
  else if (choice === 'off') body.online_payment = false
  const res = await totpAwareFetch(`/api/v1/admin/financials/invoices/bulk-reminder`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok) {
    const txt = await res.text().catch(() => '')
    error.value = `${res.status} ${txt}`
    return
  }
  selected.value = new Set()
  await load()
}

async function regeneratePdfSelected() {
  if (selected.value.size === 0) return
  if (!(await ensureFreshTotp())) return
  const items = rows.value.filter((d) => selected.value.has(d.id))
  const ok = await confirm({
    title: t('admin.faktura.sent.regenConfirmTitle'),
    body: t('admin.faktura.sent.regenBulkBody', { n: items.length }),
    details: items.map(rowLabel),
    confirmLabel: t('admin.faktura.sent.regenAction'),
    tone: 'warning',
  })
  if (!ok) return
  const ids = items.map((d) => d.id)
  const res = await totpAwareFetch(`/api/v1/admin/financials/invoices/bulk-regenerate-pdf`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ids }),
  })
  if (!res.ok) {
    const txt = await res.text().catch(() => '')
    error.value = `${res.status} ${txt}`
    return
  }
  selected.value = new Set()
  await load()
}

async function reissueKidSelected() {
  if (selected.value.size === 0) return
  if (!(await ensureFreshTotp())) return
  // Only unpaid invoices are eligible — a paid one carries a booked
  // payment/bilag keyed on its current KID (the backend also enforces this).
  const items = rows.value.filter((d) => selected.value.has(d.id) && !d.paid && !isCreditNote(d))
  if (items.length === 0) {
    error.value = t('admin.faktura.sent.reissueKidNoneEligible')
    return
  }
  const ok = await confirm({
    title: t('admin.faktura.sent.reissueKidConfirmTitle'),
    body: t('admin.faktura.sent.reissueKidBulkBody', { n: items.length }),
    details: items.map(rowLabel),
    confirmLabel: t('admin.faktura.sent.reissueKidAction'),
    tone: 'warning',
  })
  if (!ok) return
  const ids = items.map((d) => d.id)
  const res = await totpAwareFetch(`/api/v1/admin/financials/invoices/bulk-reissue-kid`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ids }),
  })
  if (!res.ok) {
    const txt = await res.text().catch(() => '')
    error.value = `${res.status} ${txt}`
    return
  }
  selected.value = new Set()
  await load()
}

async function voidSelected() {
  if (selected.value.size === 0) return
  if (!(await ensureFreshTotp())) return
  const items = rows.value.filter((d) => selected.value.has(d.id))
  const ok = await confirm({
    title: t('admin.invoiceDrafts.voidConfirmTitle'),
    body: t('admin.invoiceDrafts.voidBulkBody', { n: items.length }),
    details: items.map(rowLabel),
    confirmLabel: t('admin.invoiceDrafts.voidAction'),
    tone: 'warning',
  })
  if (!ok) return
  for (const id of items.map((d) => d.id)) await doVoid(id)
}

defineExpose({ load })
</script>

<template>
  <div>
    <div class="flex items-center justify-between">
      <p class="text-sm text-gray-600">
        <slot name="subtitle">{{ t(`admin.faktura.${status}.subtitle`) }}</slot>
      </p>
      <button
        class="inline-flex items-center gap-1 rounded-md border border-gray-300 px-3 py-1.5 text-sm hover:bg-gray-50"
        @click="load"
      >
        <RefreshCw class="h-4 w-4" /> {{ t('common.refresh') }}
      </button>
    </div>

    <p v-if="notice" class="mt-4 rounded-md bg-green-50 px-3 py-2 text-sm text-green-700">{{ notice }}</p>

    <p v-if="loading" class="mt-4 text-sm text-gray-500">{{ t('common.loading') }}…</p>
    <p v-else-if="error" class="mt-4 rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">{{ error }}</p>

    <template v-else-if="rows.length">
      <div class="mt-4 flex flex-wrap items-center gap-2">
        <input
          v-model="filterMember"
          type="search"
          :placeholder="t('admin.invoiceDrafts.filterMember')"
          class="rounded-md border border-gray-300 px-2 py-1 text-sm"
        />
        <select v-model="filterPriceItem" class="rounded-md border border-gray-300 px-2 py-1 text-sm">
          <option value="">{{ t('admin.invoiceDrafts.filterAllItems') }}</option>
          <option v-for="n in priceItemOptions" :key="n" :value="n">{{ n }}</option>
        </select>
        <select v-model.number="filterYear" class="rounded-md border border-gray-300 px-2 py-1 text-sm">
          <option value="">{{ t('admin.invoiceDrafts.filterAllYears') }}</option>
          <option v-for="y in yearOptions" :key="y" :value="y">{{ y }}</option>
        </select>
        <div v-if="showPaidChips" class="flex flex-wrap gap-1.5">
          <button
            type="button"
            class="inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium transition"
            :class="paidStatusFilter.has('paid') ? 'border-green-300 bg-green-50 text-green-800' : 'border-gray-200 bg-white text-gray-600 hover:border-gray-300'"
            @click="togglePaidStatus('paid')"
          >
            <span class="inline-block h-1.5 w-1.5 rounded-full bg-green-500" />
            {{ t('admin.faktura.sent.filterPaid') }}
            <span class="rounded-full bg-white/60 px-1.5 py-0.5 text-[10px] font-semibold tabular-nums text-gray-700">{{ paidStatusCounts.paid }}</span>
          </button>
          <button
            type="button"
            class="inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium transition"
            :class="paidStatusFilter.has('waiting') ? 'border-yellow-300 bg-yellow-50 text-yellow-800' : 'border-gray-200 bg-white text-gray-600 hover:border-gray-300'"
            @click="togglePaidStatus('waiting')"
          >
            <span class="inline-block h-1.5 w-1.5 rounded-full bg-yellow-500" />
            {{ t('admin.faktura.sent.filterWaiting') }}
            <span class="rounded-full bg-white/60 px-1.5 py-0.5 text-[10px] font-semibold tabular-nums text-gray-700">{{ paidStatusCounts.waiting }}</span>
          </button>
          <button
            type="button"
            class="inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium transition"
            :class="paidStatusFilter.has('past_due') ? 'border-red-300 bg-red-50 text-red-800' : 'border-gray-200 bg-white text-gray-600 hover:border-gray-300'"
            @click="togglePaidStatus('past_due')"
          >
            <span class="inline-block h-1.5 w-1.5 rounded-full bg-red-500" />
            {{ t('admin.faktura.sent.filterPastDue') }}
            <span class="rounded-full bg-white/60 px-1.5 py-0.5 text-[10px] font-semibold tabular-nums text-gray-700">{{ paidStatusCounts.past_due }}</span>
          </button>
          <button
            v-if="paidStatusCounts.credit > 0"
            type="button"
            class="inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium transition"
            :class="paidStatusFilter.has('credit') ? 'border-sky-300 bg-sky-50 text-sky-800' : 'border-gray-200 bg-white text-gray-600 hover:border-gray-300'"
            data-testid="credit-chip"
            @click="togglePaidStatus('credit')"
          >
            <span class="inline-block h-1.5 w-1.5 rounded-full bg-sky-500" />
            {{ t('admin.faktura.credit.filter') }}
            <span class="rounded-full bg-white/60 px-1.5 py-0.5 text-[10px] font-semibold tabular-nums text-gray-700">{{ paidStatusCounts.credit }}</span>
          </button>
        </div>
        <span class="ml-auto text-xs text-gray-500">
          {{ t('admin.invoiceDrafts.summary', { n: filtered.length, total: formatNOK(totalAmount) }) }}
        </span>
      </div>

      <div
        v-if="selected.size > 0"
        class="mt-3 flex flex-wrap items-center gap-3 rounded-md border border-brand-200 bg-brand-50 px-3 py-2 text-sm"
      >
        <span class="font-medium text-brand-900">
          {{ t('admin.invoiceDrafts.selectedCount', { n: selected.size }) }}
        </span>
        <button
          type="button"
          class="inline-flex items-center gap-1 rounded text-xs text-brand-700 hover:text-brand-900 hover:underline"
          :title="t('common.clearSelection')"
          @click="clearSelection"
        >
          <X class="h-3 w-3" />
          {{ t('common.clearSelection') }}
        </button>
        <div class="ml-auto flex flex-wrap gap-2">
          <button
            type="button"
            class="inline-flex items-center gap-1 rounded-md border border-gray-300 bg-white px-3 py-1 text-xs font-semibold transition"
            :class="copyEmailsState ? 'text-green-700' : 'text-gray-700 hover:bg-gray-50'"
            :title="t('admin.faktura.copyEmails.tooltip')"
            @click="copyEmailsSelected"
          >
            <component :is="copyEmailsState ? Check : Copy" class="h-3.5 w-3.5" />
            <template v-if="copyEmailsState">{{ t('admin.faktura.copyEmails.copied', { n: copyEmailsState.count }) }}</template>
            <template v-else>{{ t('admin.faktura.copyEmails.label') }}</template>
          </button>
          <button
            v-if="showSend"
            class="inline-flex items-center gap-1 rounded-md bg-brand-600 px-3 py-1 text-xs font-semibold text-white hover:bg-brand-700"
            @click="sendSelected"
          >
            <Send class="h-3.5 w-3.5" /> {{ t('admin.invoiceDrafts.sendSelected') }}
          </button>
          <button
            v-if="showReminder"
            class="inline-flex items-center gap-1 rounded-md bg-amber-600 px-3 py-1 text-xs font-semibold text-white hover:bg-amber-700"
            @click="sendRemindersSelected"
          >
            <Bell class="h-3.5 w-3.5" /> {{ t('admin.faktura.sent.reminderSelected') }}
          </button>
          <button
            v-if="showRegenerate"
            class="inline-flex items-center gap-1 rounded-md border border-brand-300 bg-white px-3 py-1 text-xs font-semibold text-brand-700 hover:bg-brand-50"
            @click="regeneratePdfSelected"
          >
            <RefreshCw class="h-3.5 w-3.5" /> {{ t('admin.faktura.sent.regenSelected') }}
          </button>
          <button
            v-if="showReissueKid"
            class="inline-flex items-center gap-1 rounded-md border border-brand-300 bg-white px-3 py-1 text-xs font-semibold text-brand-700 hover:bg-brand-50"
            :title="t('admin.faktura.sent.reissueKidTooltip')"
            @click="reissueKidSelected"
          >
            <KeyRound class="h-3.5 w-3.5" /> {{ t('admin.faktura.sent.reissueKidSelected') }}
          </button>
          <button
            v-if="showVoid"
            class="inline-flex items-center gap-1 rounded-md border border-amber-300 bg-white px-3 py-1 text-xs font-semibold text-amber-700 hover:bg-amber-50"
            @click="voidSelected"
          >
            <Ban class="h-3.5 w-3.5" /> {{ t('admin.invoiceDrafts.voidSelected') }}
          </button>
          <button
            v-if="showDelete"
            class="inline-flex items-center gap-1 rounded-md border border-red-300 bg-white px-3 py-1 text-xs font-semibold text-red-700 hover:bg-red-50"
            @click="deleteSelected"
          >
            <Trash2 class="h-3.5 w-3.5" /> {{ t('admin.invoiceDrafts.deleteSelected') }}
          </button>
        </div>
      </div>

      <div class="mt-3 overflow-x-auto">
        <table class="min-w-full divide-y divide-gray-200">
          <thead class="bg-gray-50">
            <tr>
              <th class="w-8 px-2 py-2 text-center">
                <input
                  type="checkbox"
                  :checked="allFilteredSelected"
                  class="h-4 w-4 rounded border-gray-300 accent-brand-600 focus:ring-1 focus:ring-brand-500"
                  @change="toggleAll"
                />
              </th>
              <th class="cursor-pointer select-none px-3 py-2 text-left text-xs font-medium uppercase tracking-wider text-gray-500 hover:text-gray-900" @click="setSort('invoice_number')">
                <span class="inline-flex items-center gap-1">#<ChevronUp v-if="sortKey === 'invoice_number' && sortDir === 'asc'" class="h-3 w-3" /><ChevronDown v-else-if="sortKey === 'invoice_number'" class="h-3 w-3" /></span>
              </th>
              <th class="cursor-pointer select-none px-3 py-2 text-left text-xs font-medium uppercase tracking-wider text-gray-500 hover:text-gray-900" @click="setSort('member_name')">
                <span class="inline-flex items-center gap-1">{{ t('admin.invoiceDrafts.member') }}<ChevronUp v-if="sortKey === 'member_name' && sortDir === 'asc'" class="h-3 w-3" /><ChevronDown v-else-if="sortKey === 'member_name'" class="h-3 w-3" /></span>
              </th>
              <th class="cursor-pointer select-none px-3 py-2 text-left text-xs font-medium uppercase tracking-wider text-gray-500 hover:text-gray-900" @click="setSort('price_item_name')">
                <span class="inline-flex items-center gap-1">{{ t('admin.invoiceDrafts.priceItem') }}<ChevronUp v-if="sortKey === 'price_item_name' && sortDir === 'asc'" class="h-3 w-3" /><ChevronDown v-else-if="sortKey === 'price_item_name'" class="h-3 w-3" /></span>
              </th>
              <th class="cursor-pointer select-none px-3 py-2 text-left text-xs font-medium uppercase tracking-wider text-gray-500 hover:text-gray-900" @click="setSort('fiscal_year')">
                <span class="inline-flex items-center gap-1">{{ t('admin.invoiceDrafts.year') }}<ChevronUp v-if="sortKey === 'fiscal_year' && sortDir === 'asc'" class="h-3 w-3" /><ChevronDown v-else-if="sortKey === 'fiscal_year'" class="h-3 w-3" /></span>
              </th>
              <th class="cursor-pointer select-none px-3 py-2 text-right text-xs font-medium uppercase tracking-wider text-gray-500 hover:text-gray-900" @click="setSort('total_amount')">
                <span class="inline-flex items-center justify-end gap-1">{{ t('admin.invoiceDrafts.amount') }}<ChevronUp v-if="sortKey === 'total_amount' && sortDir === 'asc'" class="h-3 w-3" /><ChevronDown v-else-if="sortKey === 'total_amount'" class="h-3 w-3" /></span>
              </th>
              <th class="cursor-pointer select-none px-3 py-2 text-left text-xs font-medium uppercase tracking-wider text-gray-500 hover:text-gray-900" @click="setSort('due_date')">
                <span class="inline-flex items-center gap-1">{{ t('admin.invoiceDrafts.dueDate') }}<ChevronUp v-if="sortKey === 'due_date' && sortDir === 'asc'" class="h-3 w-3" /><ChevronDown v-else-if="sortKey === 'due_date'" class="h-3 w-3" /></span>
              </th>
              <th v-if="status === 'sent'" class="cursor-pointer select-none px-3 py-2 text-left text-xs font-medium uppercase tracking-wider text-gray-500 hover:text-gray-900" @click="setSort('sent_at')">
                <span class="inline-flex items-center gap-1">{{ t('admin.faktura.sent.sentAt') }}<ChevronUp v-if="sortKey === 'sent_at' && sortDir === 'asc'" class="h-3 w-3" /><ChevronDown v-else-if="sortKey === 'sent_at'" class="h-3 w-3" /></span>
              </th>
              <th v-if="status === 'sent'" class="cursor-pointer select-none px-3 py-2 text-left text-xs font-medium uppercase tracking-wider text-gray-500 hover:text-gray-900" @click="setSort('last_reminder_at')">
                <span class="inline-flex items-center gap-1">{{ t('admin.faktura.sent.lastReminder') }}<ChevronUp v-if="sortKey === 'last_reminder_at' && sortDir === 'asc'" class="h-3 w-3" /><ChevronDown v-else-if="sortKey === 'last_reminder_at'" class="h-3 w-3" /></span>
              </th>
              <th class="px-3 py-2 text-right text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('common.actions') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-200 bg-white">
            <tr v-for="(d, idx) in sorted" :key="d.id" :class="{ 'bg-brand-50/50': selected.has(d.id) }">
              <td class="px-2 py-2 text-center">
                <input
                  type="checkbox"
                  :checked="selected.has(d.id)"
                  class="h-4 w-4 rounded border-gray-300 accent-brand-600 focus:ring-1 focus:ring-brand-500"
                  @click="onRowClick(idx, $event)"
                />
              </td>
              <td class="whitespace-nowrap px-3 py-2">
                <InvoiceNumberChip
                  v-if="showPaidChips"
                  :number="d.invoice_number"
                  :status="rowPaidStatus(d)"
                  :title="paidStatusTitle(d)"
                />
                <span v-else class="font-mono text-xs text-gray-500">{{ d.invoice_number }}</span>
              </td>
              <td class="px-3 py-2 text-sm">
                <div class="font-medium text-gray-900">
                  <RouterLink
                    v-if="d.user_id"
                    :to="{ name: 'admin-users', query: { user: d.user_id } }"
                    class="text-blue-600 hover:underline"
                  >{{ d.member_name }}</RouterLink>
                  <template v-else>{{ d.member_name }}</template>
                </div>
                <div v-if="d.member_phone" class="text-xs text-gray-500">{{ d.member_phone }}</div>
                <div class="text-xs text-gray-500">{{ d.member_email }}</div>
                <span
                  v-if="status === 'sent' && d.online_paid_at && !d.paid"
                  class="mt-1 inline-flex items-center rounded-full bg-orange-50 px-1.5 py-0.5 text-[10px] font-medium text-orange-700 ring-1 ring-orange-200"
                  :title="t('admin.faktura.sent.onlineReceivedTitle')"
                >
                  {{ t('admin.faktura.sent.onlineReceived') }}
                </span>
                <span
                  v-if="d.credit_state && d.credit_state !== 'refunded'"
                  class="mt-1 inline-flex items-center rounded-full px-1.5 py-0.5 text-[10px] font-medium ring-1"
                  :class="d.credit_state === 'credit_owed'
                    ? 'bg-amber-50 text-amber-800 ring-amber-200'
                    : d.credit_state === 'settled_pair' || d.credit_state === 'refund'
                      ? 'bg-sky-50 text-sky-800 ring-sky-200'
                      : 'bg-gray-50 text-gray-700 ring-gray-200'"
                  data-testid="credit-badge"
                >
                  {{ t(`admin.faktura.credit.state.${creditStateKey(d)}`, { number: d.credits_invoice_number }) }}
                </span>
                <span
                  v-else-if="d.credited_by_invoice_number != null"
                  class="mt-1 inline-flex items-center rounded-full bg-sky-50 px-1.5 py-0.5 text-[10px] font-medium text-sky-800 ring-1 ring-sky-200"
                >
                  {{ t('admin.faktura.credit.creditedBy', { number: d.credited_by_invoice_number }) }}
                </span>
                <div
                  v-if="refundChipKind(d.refund_state) || disputeChipKind(d.dispute_status) || d.reissued_invoice_number != null || d.reissue_of_invoice_number != null"
                  class="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1"
                  data-testid="refund-info"
                >
                  <RefundStateChip
                    v-if="refundChipKind(d.refund_state)"
                    :kind="refundChipKind(d.refund_state)!"
                    :amount="d.refunded_amount"
                    :href="d.payment_url"
                  />
                  <RefundStateChip
                    v-if="disputeChipKind(d.dispute_status)"
                    :kind="disputeChipKind(d.dispute_status)!"
                    :due-by="d.dispute_due_by"
                    :href="d.dispute_url"
                  />
                  <template v-for="n in d.refund_credit_notes ?? []" :key="n.id">
                    <a
                      v-if="n.has_pdf"
                      :href="`/api/v1/admin/financials/invoices/${n.id}/pdf`"
                      target="_blank"
                      rel="noopener"
                      class="-my-3.5 inline-flex items-center py-3.5 text-[11px] font-medium text-sky-700 hover:underline"
                      data-testid="refund-credit-note"
                    >{{ t('admin.refund.creditNote', { number: n.number }) }}</a>
                    <span v-else class="text-[11px] text-gray-600" data-testid="refund-credit-note">{{ t('admin.refund.creditNote', { number: n.number }) }}</span>
                  </template>
                  <span v-if="d.reissued_invoice_number != null" class="text-[11px] font-medium text-gray-700" data-testid="reissued-as">
                    {{ t('admin.refund.reissue.reissuedAs', { number: d.reissued_invoice_number }) }}
                  </span>
                  <span v-if="d.reissue_of_invoice_number != null" class="text-[11px] font-medium text-gray-700" data-testid="reissue-of">
                    {{ t('admin.refund.reissue.reissueOf', { number: d.reissue_of_invoice_number }) }}
                  </span>
                  <!-- The actions column is off-screen on a phone or tablet (the table
                       scrolls sideways), so the action also sits here below lg. -->
                  <div v-if="canAct && d.can_reissue" class="lg:hidden">
                    <ReissueAfterRefundButton
                      :invoice-id="d.id"
                      :invoice-number="d.invoice_number"
                      :amount="Number(d.total_amount)"
                      @reissued="onReissued"
                      @failed="onReissueFailed"
                    />
                  </div>
                </div>
              </td>
              <td class="px-3 py-2 text-sm text-gray-700">{{ d.price_item_name || d.description || '—' }}</td>
              <td class="whitespace-nowrap px-3 py-2 text-sm text-gray-600">{{ d.fiscal_year ?? '—' }}</td>
              <td class="whitespace-nowrap px-3 py-2 text-right text-sm tabular-nums">{{ formatNOK(Number(d.total_amount)) }}</td>
              <td class="whitespace-nowrap px-3 py-2 text-sm text-gray-600">{{ isCreditNote(d) ? '—' : d.due_date }}</td>
              <td v-if="status === 'sent'" class="whitespace-nowrap px-3 py-2 text-sm text-gray-600">
                {{ d.sent_at ? formatDate(d.sent_at) : '—' }}
              </td>
              <td v-if="status === 'sent'" class="whitespace-nowrap px-3 py-2 text-sm text-gray-600">
                {{ d.last_reminder_at ? formatDateTime(d.last_reminder_at) : '—' }}
              </td>
              <td class="whitespace-nowrap px-3 py-2 text-right text-sm">
                <div class="flex justify-end gap-2">
                  <a
                    :href="`/api/v1/admin/financials/invoices/${d.id}/pdf`"
                    target="_blank"
                    rel="noopener"
                    class="text-gray-500 hover:text-gray-800"
                    :title="t('admin.invoiceDrafts.previewPdf')"
                  >
                    <FileDown class="h-4 w-4" />
                  </a>
                  <FakturaArchiveButton :invoice-id="d.id" />
                  <DeliveryLogButton v-if="canAct && status === 'sent'" :invoice-id="d.id" />
                  <button
                    v-if="showSend"
                    class="text-brand-600 hover:text-brand-800 disabled:opacity-50"
                    :disabled="busyIds.has(d.id)"
                    :title="t('admin.invoiceDrafts.send')"
                    @click="sendOne(d)"
                  >
                    <Send class="h-4 w-4" />
                  </button>
                  <button
                    v-if="showResend"
                    class="text-emerald-600 hover:text-emerald-800 disabled:opacity-50"
                    :disabled="busyIds.has(d.id)"
                    :title="t('admin.faktura.sent.resend')"
                    @click="resendOne(d)"
                  >
                    <Send class="h-4 w-4" />
                  </button>
                  <select
                    v-if="showPayLink && !d.paid && !d.online_paid_at"
                    class="rounded-md border border-gray-300 bg-white px-1.5 py-1 text-xs text-gray-700 disabled:opacity-50"
                    :disabled="busyIds.has(d.id)"
                    :title="t('admin.faktura.sent.onlinePayment.label')"
                    :value="optInToStr(d.online_payment_opt_in)"
                    @change="setOnlinePayment(d, ($event.target as HTMLSelectElement).value)"
                  >
                    <option value="auto">{{ t('admin.faktura.sent.onlinePayment.auto') }}</option>
                    <option value="on">{{ t('admin.faktura.sent.onlinePayment.on') }}</option>
                    <option value="off">{{ t('admin.faktura.sent.onlinePayment.off') }}</option>
                  </select>
                  <button
                    v-if="showPayLink && !d.paid && !d.online_paid_at"
                    class="text-indigo-600 hover:text-indigo-800 disabled:opacity-50"
                    :disabled="busyIds.has(d.id)"
                    :title="t('admin.payLink.title')"
                    @click="copyPayLink(d)"
                  >
                    <Link2 class="h-4 w-4" />
                  </button>
                  <RefundOrderButton v-if="canAct && d.refund_order_id" compact :order-id="d.refund_order_id" @refunded="load" />
                  <div v-if="canAct && d.can_reissue" class="hidden lg:block">
                    <ReissueAfterRefundButton
                      :invoice-id="d.id"
                      :invoice-number="d.invoice_number"
                      :amount="Number(d.total_amount)"
                      @reissued="onReissued"
                      @failed="onReissueFailed"
                    />
                  </div>
                  <button
                    v-if="canAct && props.status !== 'draft' && d.credit_state === 'unlinked'"
                    class="text-sky-600 hover:text-sky-800 disabled:opacity-50"
                    :disabled="busyIds.has(d.id)"
                    :title="t('admin.faktura.credit.settleAction')"
                    data-testid="credit-settle"
                    @click="settleCredit(d)"
                  >
                    <Scale class="h-4 w-4" />
                  </button>
                  <button
                    v-if="canAct && (d.credit_state === 'settled_pair' || d.credit_state === 'credit_owed')"
                    class="text-gray-500 hover:text-gray-800 disabled:opacity-50"
                    :disabled="busyIds.has(d.id)"
                    :title="t('admin.faktura.credit.unlinkAction')"
                    data-testid="credit-unlink"
                    @click="unlinkCredit(d)"
                  >
                    <Undo2 class="h-4 w-4" />
                  </button>
                  <button
                    v-if="showVoid && !d.refund_document"
                    class="text-amber-600 hover:text-amber-800 disabled:opacity-50"
                    :disabled="busyIds.has(d.id)"
                    :title="t('admin.invoiceDrafts.void')"
                    @click="voidOne(d)"
                  >
                    <Ban class="h-4 w-4" />
                  </button>
                  <button
                    v-if="showDelete"
                    class="text-red-600 hover:text-red-800 disabled:opacity-50"
                    :disabled="busyIds.has(d.id)"
                    :title="t('admin.invoiceDrafts.delete')"
                    @click="deleteOne(d)"
                  >
                    <Trash2 class="h-4 w-4" />
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>

    <p v-else class="mt-6 text-sm text-gray-500">{{ t(`admin.faktura.${status}.empty`) }}</p>

    <CreditNoteSearchModal
      v-model:open="creditSearchOpen"
      :credit-note="creditSearch?.note ?? null"
      :notice="creditSearch?.notice"
      :link-error="creditSearch?.error"
      :busy="creditSearch ? busyIds.has(creditSearch.note.id) : false"
      @pick="pickCredit"
    />
  </div>
</template>
