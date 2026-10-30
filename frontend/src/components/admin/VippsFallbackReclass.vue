<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RefreshCw } from 'lucide-vue-next'
import { useConfirm } from '@/stores/confirm'
import { useFreshTotp } from '@/composables/useFreshTotp'
import { ApiError } from '@/lib/errors'
import {
  previewVippsFallbackReclass,
  useApplyVippsFallbackReclass,
  type VippsFallbackReclassItem,
  type VippsFallbackReclassResult,
} from '@/composables/useAccounting'

// Moves posted Vipps charges parked on 3900 to the account of their price
// item (BRY-557): preview first, then an apply that needs a fresh TOTP and a
// confirmation listing counts and totals. Charges that matched a faktura or no
// single price item stay, each with its reason.
const { t } = useI18n()
const askConfirm = useConfirm()
const { ensureFreshTotp } = useFreshTotp()
const applyMutation = useApplyVippsFallbackReclass()

const loading = ref(false)
const applying = ref(false)
const preview = ref<VippsFallbackReclassResult | null>(null)
const error = ref('')

const proposed = computed(() => preview.value?.items.filter((i) => i.status === 'reclassify') ?? [])
const done = computed(() => preview.value?.applied === true)

// The server's error text is not translated: a known code gets its own
// message, anything else the generic one.
function errorText(err: unknown): string {
  if (err instanceof ApiError && err.code === 'auto_assign_off') {
    return t('admin.accounting.vippsFallbackReclass.autoAssignOff')
  }
  return t('common.error')
}

function kr(amount: number): string {
  return amount.toLocaleString('nb-NO', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

async function load() {
  error.value = ''
  loading.value = true
  try {
    preview.value = await previewVippsFallbackReclass()
  } catch (err) {
    error.value = errorText(err)
  } finally {
    loading.value = false
  }
}

function close() {
  preview.value = null
  error.value = ''
}

// The apply names each charge with the account the preview showed, so it
// moves only what the treasurer saw; the server re-checks every charge.
async function apply() {
  const current = preview.value
  if (!current || !proposed.value.length) return
  // Step-up first, at click time, then the confirmation.
  if (!(await ensureFreshTotp())) return
  const ok = await askConfirm({
    title: t('admin.accounting.vippsFallbackReclass.confirmTitle', { count: proposed.value.length }),
    body: t('admin.accounting.vippsFallbackReclass.confirmBody', {
      needsTildel: current.needs_tildel,
      stays: current.stays,
      clearing: current.clearing_account,
    }),
    details: current.totals.map((x) =>
      t('admin.accounting.vippsFallbackReclass.totalLine', {
        account: x.account_code,
        count: x.count,
        amount: kr(x.amount),
      }),
    ),
    confirmLabel: t('admin.accounting.vippsFallbackReclass.confirm'),
    cancelLabel: t('common.cancel'),
    tone: 'warning',
  })
  if (!ok) return

  error.value = ''
  applying.value = true
  const targets = Object.fromEntries(proposed.value.map((i) => [i.entry_id, i.to ?? '']))
  try {
    preview.value = await new Promise<VippsFallbackReclassResult>((resolve, reject) => {
      applyMutation.mutate({ targets }, { onSuccess: resolve, onError: reject })
    })
  } catch (err) {
    error.value = errorText(err)
  } finally {
    applying.value = false
  }
}

function reasonText(item: VippsFallbackReclassItem): string {
  if (item.status === 'needs_tildel') {
    return t('admin.accounting.vippsFallbackReclass.needsTildel', { number: item.invoice_number ?? '' })
  }
  // Every code has its own text in every locale; the server's English
  // reason is never shown.
  return t(`admin.accounting.vippsFallbackReclass.reason.${item.reason_code ?? 'failed'}`, {
    number: item.invoice_number ?? '',
  })
}

function outcomeText(item: VippsFallbackReclassItem): string {
  switch (item.outcome) {
    case 'reclassified':
      return t('admin.accounting.vippsFallbackReclass.outcomeReclassified', {
        bilag: item.new_entry_number ?? '',
        reversal: item.reversal_entry_number ?? '',
      })
    case 'skipped':
      return `${t('admin.accounting.vippsFallbackReclass.outcomeSkipped')}: ${reasonText(item)}`
    case 'failed':
      return `${t('admin.accounting.vippsFallbackReclass.outcomeFailed')}: ${reasonText(item)}`
    default:
      return ''
  }
}

// What the Forslag column says: the proposed account and price item, or why
// the charge stays.
function proposalText(item: VippsFallbackReclassItem): string {
  if (item.status === 'reclassify') {
    return item.price_item ? `${item.to} · ${item.price_item}` : (item.to ?? '')
  }
  return reasonText(item)
}
</script>

<template>
  <div class="mt-3" data-testid="vipps-fallback-reclass">
    <button
      class="inline-flex min-h-11 w-full items-center justify-center gap-1.5 rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50 sm:w-auto"
      :disabled="loading || applying"
      :title="t('admin.accounting.vippsFallbackReclass.tooltip')"
      data-testid="vipps-fallback-reclass-open"
      @click="load"
    >
      <RefreshCw class="h-4 w-4" :class="{ 'animate-spin': loading }" />
      {{ t('admin.accounting.vippsFallbackReclass.button') }}
    </button>

    <p v-if="error" class="mt-2 text-sm text-red-600" role="alert" data-testid="vipps-fallback-reclass-error">
      {{ error }}
    </p>

    <section
      v-if="preview"
      class="mt-3 rounded-lg border border-gray-200 bg-gray-50 p-3 sm:p-4"
      :aria-label="t('admin.accounting.vippsFallbackReclass.button')"
      data-testid="vipps-fallback-reclass-panel"
    >
      <p v-if="!preview.auto_assign_enabled" class="rounded-md bg-amber-50 px-3 py-2 text-sm text-amber-900" role="alert">
        {{ t('admin.accounting.vippsFallbackReclass.autoAssignOff') }}
      </p>
      <p v-if="!preview.items.length" class="text-sm text-gray-600" data-testid="vipps-fallback-reclass-empty">
        {{ t('admin.accounting.vippsFallbackReclass.nothing') }}
      </p>

      <template v-else>
        <p v-if="done" class="text-sm font-medium text-green-700" data-testid="vipps-fallback-reclass-result">
          {{ t('admin.accounting.vippsFallbackReclass.done', { count: preview.reclassified }) }}
          <span v-if="preview.skipped || preview.failed || preview.stale_ids.length" class="font-normal text-amber-700">
            {{ t('admin.accounting.vippsFallbackReclass.doneNotes', {
              skipped: preview.skipped,
              failed: preview.failed,
              stale: preview.stale_ids.length,
            }) }}
          </span>
        </p>
        <p v-else class="text-sm text-gray-700" data-testid="vipps-fallback-reclass-summary">
          {{ t('admin.accounting.vippsFallbackReclass.summary', {
            proposed: preview.proposed,
            needsTildel: preview.needs_tildel,
            stays: preview.stays,
          }) }}
        </p>

        <div v-if="preview.totals.length" class="mt-3">
          <p class="text-sm font-medium text-gray-700">
            {{ done ? t('admin.accounting.vippsFallbackReclass.movedHeading') : t('admin.accounting.vippsFallbackReclass.totalsHeading') }}
          </p>
          <ul class="mt-1 grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
            <li
              v-for="x in preview.totals"
              :key="x.account_code"
              class="rounded-md border border-gray-200 bg-white px-3 py-2 text-sm"
              :data-testid="`vipps-fallback-reclass-total-${x.account_code}`"
            >
              {{ t('admin.accounting.vippsFallbackReclass.totalLine', {
                account: x.account_code,
                count: x.count,
                amount: kr(x.amount),
              }) }}
            </li>
          </ul>
        </div>

        <p v-if="preview.closed_period_charges" class="mt-3 text-sm text-gray-600">
          {{ t('admin.accounting.vippsFallbackReclass.closedNote', {
            count: preview.closed_period_charges,
            amount: kr(preview.closed_period_amount),
          }) }}
        </p>

        <!-- Wide screens: a table. Below md: one stacked card per charge. -->
        <div class="mt-3 hidden md:block" data-testid="vipps-fallback-reclass-table">
          <table class="min-w-full divide-y divide-gray-200 text-sm">
            <thead class="text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              <tr>
                <th class="py-2 pr-3">{{ t('admin.accounting.vippsFallbackReclass.colBilag') }}</th>
                <th class="py-2 pr-3">{{ t('admin.accounting.vippsFallbackReclass.colDate') }}</th>
                <th class="py-2 pr-3 text-right">{{ t('admin.accounting.vippsFallbackReclass.colAmount') }}</th>
                <th class="py-2 pr-3">{{ t('admin.accounting.vippsFallbackReclass.colFrom') }}</th>
                <th class="py-2">{{ t('admin.accounting.vippsFallbackReclass.colProposal') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-200 bg-white">
              <tr v-for="item in preview.items" :key="item.entry_id" :data-testid="`vipps-fallback-reclass-row-${item.entry_id}`">
                <td class="py-2 pr-3 font-mono">{{ item.entry_number }}</td>
                <td class="whitespace-nowrap py-2 pr-3">{{ item.date }}</td>
                <td class="whitespace-nowrap py-2 pr-3 text-right">{{ kr(item.amount) }}</td>
                <td class="py-2 pr-3 font-mono">{{ item.from }}</td>
                <td class="py-2" :class="item.status === 'reclassify' ? 'text-gray-900' : 'text-gray-500'">
                  {{ proposalText(item) }}
                  <span v-if="item.outcome" class="block text-xs" :class="item.outcome === 'reclassified' ? 'text-green-700' : 'text-amber-700'">{{ outcomeText(item) }}</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <ul class="mt-3 space-y-2 md:hidden" data-testid="vipps-fallback-reclass-cards">
          <li
            v-for="item in preview.items"
            :key="item.entry_id"
            class="rounded-md border border-gray-200 bg-white p-3 text-sm"
          >
            <div class="flex items-baseline justify-between gap-2">
              <span class="font-mono">#{{ item.entry_number }} · {{ item.date }}</span>
              <span class="whitespace-nowrap font-medium">{{ kr(item.amount) }} kr</span>
            </div>
            <p class="mt-1" :class="item.status === 'reclassify' ? 'text-gray-900' : 'text-gray-500'">
              <span class="font-mono">{{ item.from }}</span>
              <template v-if="item.status === 'reclassify'"> → </template>
              <template v-else> · </template>
              {{ proposalText(item) }}
            </p>
            <p v-if="item.outcome" class="mt-1 text-xs" :class="item.outcome === 'reclassified' ? 'text-green-700' : 'text-amber-700'">{{ outcomeText(item) }}</p>
          </li>
        </ul>
      </template>

      <div class="mt-4 flex flex-col gap-2 sm:flex-row sm:justify-end">
        <button
          class="inline-flex min-h-11 items-center justify-center rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
          data-testid="vipps-fallback-reclass-close"
          @click="close"
        >
          {{ t('common.close') }}
        </button>
        <button
          v-if="!done"
          class="inline-flex min-h-11 items-center justify-center rounded-md bg-brand-600 px-4 py-2 text-sm font-medium text-white hover:bg-brand-700 disabled:opacity-50"
          :disabled="applying || !proposed.length || !preview.auto_assign_enabled"
          data-testid="vipps-fallback-reclass-apply"
          @click="apply"
        >
          {{ t('admin.accounting.vippsFallbackReclass.apply', { count: proposed.length }) }}
        </button>
      </div>
    </section>
  </div>
</template>
