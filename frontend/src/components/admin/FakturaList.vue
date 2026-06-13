<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { FileDown, Send, Trash2, Ban, Bell, RefreshCw, Copy, Check } from 'lucide-vue-next'
import FakturaArchiveButton from '@/components/admin/FakturaArchiveButton.vue'
import DeliveryLogButton from '@/components/admin/DeliveryLogButton.vue'
import { useConfirm } from '@/stores/confirm'
import { useFreshTotp } from '@/composables/useFreshTotp'

interface Row {
  id: string
  invoice_number: number
  user_id: string
  member_name: string
  member_email: string
  total_amount: number
  issue_date: string
  due_date: string
  price_item_name: string
  fiscal_year: number | null
  description: string
  created_at: string
  sent_at: string | null
  last_notified_at: string | null
  status: string
  paid: boolean
}

type PaidStatus = 'paid' | 'waiting' | 'past_due'

const props = defineProps<{
  /** Backend filter — drives which rows are listed and which actions are shown. */
  status: 'draft' | 'sent' | 'voided'
}>()

const { t } = useI18n()
const confirm = useConfirm()
const { ensureFreshTotp, totpAwareFetch } = useFreshTotp()

const rows = ref<Row[]>([])
const loading = ref(true)
const error = ref<string | null>(null)
const busyIds = ref<Set<string>>(new Set())
const selected = ref<Set<string>>(new Set())
const filterMember = ref('')
const filterPriceItem = ref('')
const filterYear = ref<number | ''>('')
const paidStatusFilter = ref<Set<PaidStatus>>(new Set())

function rowPaidStatus(d: Row): PaidStatus {
  if (d.paid) return 'paid'
  return new Date(d.due_date) < new Date(new Date().toDateString()) ? 'past_due' : 'waiting'
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
  const acc = { paid: 0, waiting: 0, past_due: 0 } as Record<PaidStatus, number>
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

const showSend = computed(() => props.status === 'draft')
const showResend = computed(() => props.status === 'sent')
const showDelete = computed(() => props.status !== 'sent') // backend rejects DELETE on sent open invoices
const showVoid = computed(() => props.status !== 'voided')
const showReminder = computed(() => props.status === 'sent')
const showRegenerate = computed(() => props.status === 'sent')

function toggle(id: string) {
  const next = new Set(selected.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  selected.value = next
}
function toggleAll() {
  const next = new Set(selected.value)
  if (allFilteredSelected.value) for (const d of filtered.value) next.delete(d.id)
  else for (const d of filtered.value) next.add(d.id)
  selected.value = next
}

function rowLabel(d: Row): string {
  return `#${d.invoice_number} — ${d.member_name} (${formatNOK(Number(d.total_amount))})`
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
    }
  } finally {
    busyIds.value.delete(id)
  }
}

async function doDelete(id: string) {
  busyIds.value.add(id)
  try {
    const res = await totpAwareFetch(`/api/v1/admin/financials/invoices/${id}`, { method: 'DELETE' })
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
  const items = rows.value.filter((d) => selected.value.has(d.id) && !d.paid)
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
  })
  if (!ok) return
  const ids = items.map((d) => d.id)
  const res = await totpAwareFetch(`/api/v1/admin/financials/invoices/bulk-reminder`, {
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

function formatNOK(n: number): string {
  return new Intl.NumberFormat('nb-NO', { style: 'currency', currency: 'NOK' }).format(n)
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
        <div v-if="status === 'sent'" class="flex flex-wrap gap-1.5">
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
        </div>
        <span class="ml-auto text-xs text-gray-500">
          {{ t('admin.invoiceDrafts.summary', { n: filtered.length, total: formatNOK(totalAmount) }) }}
        </span>
      </div>

      <div
        v-if="selected.size > 0"
        class="mt-3 flex flex-wrap items-center gap-3 rounded-md border border-blue-200 bg-blue-50 px-3 py-2 text-sm"
      >
        <span class="font-medium text-blue-900">
          {{ t('admin.invoiceDrafts.selectedCount', { n: selected.size }) }}
        </span>
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
            class="inline-flex items-center gap-1 rounded-md bg-blue-600 px-3 py-1 text-xs font-semibold text-white hover:bg-blue-700"
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
            class="inline-flex items-center gap-1 rounded-md border border-blue-300 bg-white px-3 py-1 text-xs font-semibold text-blue-700 hover:bg-blue-50"
            @click="regeneratePdfSelected"
          >
            <RefreshCw class="h-3.5 w-3.5" /> {{ t('admin.faktura.sent.regenSelected') }}
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
                  class="rounded border-gray-300"
                  @change="toggleAll"
                />
              </th>
              <th class="px-3 py-2 text-left text-xs font-medium uppercase tracking-wider text-gray-500">#</th>
              <th class="px-3 py-2 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.invoiceDrafts.member') }}</th>
              <th class="px-3 py-2 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.invoiceDrafts.priceItem') }}</th>
              <th class="px-3 py-2 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.invoiceDrafts.year') }}</th>
              <th class="px-3 py-2 text-right text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.invoiceDrafts.amount') }}</th>
              <th class="px-3 py-2 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.invoiceDrafts.dueDate') }}</th>
              <th v-if="status === 'sent'" class="px-3 py-2 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.faktura.sent.sentAt') }}</th>
              <th v-if="status === 'sent'" class="px-3 py-2 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.faktura.sent.lastNotified') }}</th>
              <th class="px-3 py-2 text-right text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('common.actions') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-200 bg-white">
            <tr v-for="d in filtered" :key="d.id" :class="{ 'bg-blue-50/50': selected.has(d.id) }">
              <td class="px-2 py-2 text-center">
                <input
                  type="checkbox"
                  :checked="selected.has(d.id)"
                  class="rounded border-gray-300"
                  @change="toggle(d.id)"
                />
              </td>
              <td class="whitespace-nowrap px-3 py-2 font-mono text-xs text-gray-500">{{ d.invoice_number }}</td>
              <td class="px-3 py-2 text-sm">
                <div class="font-medium text-gray-900">{{ d.member_name }}</div>
                <div class="text-xs text-gray-500">{{ d.member_email }}</div>
              </td>
              <td class="px-3 py-2 text-sm text-gray-700">{{ d.price_item_name || d.description || '—' }}</td>
              <td class="whitespace-nowrap px-3 py-2 text-sm text-gray-600">{{ d.fiscal_year ?? '—' }}</td>
              <td class="whitespace-nowrap px-3 py-2 text-right text-sm tabular-nums">{{ formatNOK(Number(d.total_amount)) }}</td>
              <td class="whitespace-nowrap px-3 py-2 text-sm text-gray-600">{{ d.due_date }}</td>
              <td v-if="status === 'sent'" class="whitespace-nowrap px-3 py-2 text-sm text-gray-600">
                {{ d.sent_at ? new Date(d.sent_at).toLocaleDateString() : '—' }}
              </td>
              <td v-if="status === 'sent'" class="whitespace-nowrap px-3 py-2 text-sm text-gray-600">
                {{ d.last_notified_at ? new Date(d.last_notified_at).toLocaleString() : '—' }}
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
                  <DeliveryLogButton v-if="status === 'sent'" :invoice-id="d.id" />
                  <button
                    v-if="showSend"
                    class="text-blue-600 hover:text-blue-800 disabled:opacity-50"
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
                  <button
                    v-if="showVoid"
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
  </div>
</template>
