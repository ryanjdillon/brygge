<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { formatNOK } from '@/lib/format'
import { RefreshCw, ArrowLeftRight, X, CheckCircle2 } from 'lucide-vue-next'
import {
  usePendingRefunds,
  useSuggestRefundOutbound,
  usePairRefundMutation,
  type RefundPendingRow,
} from '@/composables/useBankReconcile'

const { t } = useI18n()

const { data: rows, isLoading, refetch } = usePendingRefunds()
const pairMutation = usePairRefundMutation()

// ── Pairing modal state ─────────────────────────────────────────────────────

const activeRow = ref<RefundPendingRow | null>(null)
const manualOutboundId = ref('')
const pairError = ref<string | null>(null)

const activeRowId = computed(() => activeRow.value?.id ?? null)
const { data: suggestions, isFetching: loadingSuggestions } = useSuggestRefundOutbound(activeRowId)

function openPairing(row: RefundPendingRow) {
  activeRow.value = row
  manualOutboundId.value = ''
  pairError.value = null
}

function closePairing() {
  activeRow.value = null
  manualOutboundId.value = ''
  pairError.value = null
}

async function confirmPairing(outboundRowId: string) {
  if (!activeRow.value) return
  pairError.value = null
  try {
    await pairMutation.mutateAsync({ rowId: activeRow.value.id, outboundRowId })
    closePairing()
  } catch (e) {
    pairError.value = (e as Error).message
  }
}

async function confirmManual() {
  if (!manualOutboundId.value.trim()) return
  await confirmPairing(manualOutboundId.value.trim())
}

// ── Helpers ─────────────────────────────────────────────────────────────────

function reasonLabel(reason: string): string {
  const map: Record<string, string> = {
    double_payment: t('admin.bankRows.dismissReasons.double_payment'),
    overpayment: t('admin.bankRows.dismissReasons.overpayment'),
    refund_or_credit: t('admin.bankRows.dismissReasons.refund_or_credit'),
  }
  return map[reason] ?? reason
}

function fmtDate(d: string): string {
  return d?.slice(0, 10) ?? ''
}
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between">
      <p class="text-sm text-gray-500">
        {{ t('admin.bankImports.refundTabDescription') }}
      </p>
      <button
        class="inline-flex items-center gap-1.5 rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm text-gray-700 hover:bg-gray-50"
        @click="refetch()"
      >
        <RefreshCw class="h-3.5 w-3.5" :class="{ 'animate-spin': isLoading }" />
        {{ t('common.refresh') }}
      </button>
    </div>

    <!-- Empty state -->
    <div
      v-if="!isLoading && (!rows || rows.length === 0)"
      class="flex flex-col items-center gap-2 rounded-lg border border-dashed border-gray-300 py-14 text-gray-400"
    >
      <CheckCircle2 class="h-8 w-8 text-green-400" />
      <p class="text-sm">{{ t('admin.bankImports.refundEmpty') }}</p>
    </div>

    <!-- Refund queue table -->
    <div v-else class="overflow-x-auto rounded-lg border border-gray-200 bg-white">
      <table class="min-w-full divide-y divide-gray-200 text-sm">
        <thead class="bg-gray-50 text-left text-xs font-medium uppercase tracking-wide text-gray-500">
          <tr>
            <th class="px-3 py-2">{{ t('admin.bankImports.colDate') }}</th>
            <th class="px-3 py-2">{{ t('admin.bankImports.colCounterpart') }}</th>
            <th class="px-3 py-2">{{ t('admin.bankImports.colDescription') }}</th>
            <th class="px-3 py-2 text-right">{{ t('admin.bankImports.colAmount') }}</th>
            <th class="px-3 py-2">{{ t('admin.bankImports.colReason') }}</th>
            <th class="px-3 py-2"></th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-100">
          <tr v-for="row in rows" :key="row.id" class="hover:bg-gray-50">
            <td class="whitespace-nowrap px-3 py-2 tabular-nums text-gray-600">
              {{ fmtDate(row.row_date) }}
            </td>
            <td class="max-w-[160px] truncate px-3 py-2 font-medium" :title="row.counterpart">
              {{ row.counterpart || '–' }}
            </td>
            <td class="max-w-[200px] truncate px-3 py-2 text-gray-500" :title="row.description">
              {{ row.description || '–' }}
            </td>
            <td class="whitespace-nowrap px-3 py-2 text-right tabular-nums font-medium text-green-700">
              {{ formatNOK(row.amount) }}
            </td>
            <td class="px-3 py-2">
              <span class="inline-flex rounded bg-red-100 px-2 py-0.5 text-xs font-medium text-red-700">
                {{ reasonLabel(row.dismissed_reason) }}
              </span>
            </td>
            <td class="px-3 py-2">
              <button
                class="inline-flex items-center gap-1.5 rounded-md border border-brand-300 bg-white px-2.5 py-1 text-xs font-medium text-brand-700 hover:bg-brand-50"
                @click="openPairing(row)"
              >
                <ArrowLeftRight class="h-3.5 w-3.5" />
                {{ t('admin.bankImports.pairRefund') }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Pairing modal -->
    <Teleport to="body">
      <div
        v-if="activeRow"
        class="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
        @click.self="closePairing"
      >
        <div class="w-full max-w-lg rounded-xl bg-white p-6 shadow-xl">
          <div class="mb-4 flex items-start justify-between gap-3">
            <div>
              <h3 class="text-base font-semibold text-gray-900">
                {{ t('admin.bankImports.pairRefundTitle') }}
              </h3>
              <p class="mt-0.5 text-sm text-gray-500">
                {{ t('admin.bankImports.pairRefundDesc') }}
              </p>
            </div>
            <button class="rounded p-1 text-gray-400 hover:bg-gray-100" @click="closePairing">
              <X class="h-4 w-4" />
            </button>
          </div>

          <!-- Original row summary -->
          <div class="mb-4 rounded-lg bg-gray-50 px-4 py-3 text-sm">
            <div class="flex items-center justify-between">
              <span class="text-gray-500">{{ t('admin.bankImports.originalRow') }}</span>
              <span class="font-semibold tabular-nums text-green-700">{{ formatNOK(activeRow.amount) }}</span>
            </div>
            <div class="mt-1 text-xs text-gray-500">
              {{ fmtDate(activeRow.row_date) }} · {{ activeRow.counterpart || activeRow.description || '–' }}
            </div>
          </div>

          <!-- Auto-suggestions -->
          <div class="mb-4">
            <p class="mb-2 text-xs font-medium uppercase tracking-wide text-gray-500">
              {{ t('admin.bankImports.suggestedOutbound') }}
            </p>
            <div v-if="loadingSuggestions" class="text-sm text-gray-400">{{ t('common.loading') }}…</div>
            <div v-else-if="!suggestions || suggestions.length === 0" class="text-sm text-gray-400">
              {{ t('admin.bankImports.noSuggestions') }}
            </div>
            <div v-else class="space-y-1.5">
              <button
                v-for="c in suggestions"
                :key="c.id"
                class="w-full rounded-lg border border-gray-200 px-3 py-2 text-left text-sm hover:border-brand-300 hover:bg-brand-50"
                :disabled="pairMutation.isPending.value"
                @click="confirmPairing(c.id)"
              >
                <div class="flex items-center justify-between">
                  <span class="font-medium">{{ c.counterpart || c.description || '–' }}</span>
                  <span class="tabular-nums text-red-600">{{ formatNOK(c.amount) }}</span>
                </div>
                <div class="text-xs text-gray-500">{{ fmtDate(c.row_date) }}</div>
              </button>
            </div>
          </div>

          <!-- Manual entry -->
          <div>
            <p class="mb-1.5 text-xs font-medium uppercase tracking-wide text-gray-500">
              {{ t('admin.bankImports.manualOutboundId') }}
            </p>
            <div class="flex gap-2">
              <input
                v-model="manualOutboundId"
                type="text"
                :placeholder="t('admin.bankImports.manualOutboundPlaceholder')"
                class="flex-1 rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-brand-500 focus:outline-none"
              />
              <button
                :disabled="!manualOutboundId.trim() || pairMutation.isPending.value"
                class="rounded-md bg-brand-600 px-4 py-2 text-sm font-medium text-white hover:bg-brand-700 disabled:opacity-40"
                @click="confirmManual"
              >
                {{ t('admin.bankImports.confirmPair') }}
              </button>
            </div>
          </div>

          <p v-if="pairError" class="mt-3 text-sm text-red-600">{{ pairError }}</p>
        </div>
      </div>
    </Teleport>
  </div>
</template>
