<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Modal from '@/components/ui/Modal.vue'
import { useFreshTotp } from '@/composables/useFreshTotp'
import { formatNOK, formatDate } from '@/lib/format'

// Manual search for the faktura a credit note settles (BRY-393), for when
// the same-payer, same-amount candidates come up empty, e.g. an imported
// credit note without a member. The server still refuses a partial credit
// and asks for confirmation across payers; this list only shows the
// mismatches up front.

export interface CreditSearchHit {
  id: string
  invoice_number: number
  total_amount: number
  issue_date: string
  settled: boolean
  imported: boolean
  payer_name: string
  same_amount: boolean
  same_payer: boolean
}

const props = defineProps<{
  open: boolean
  creditNote: { id: string; invoice_number: number; total_amount: number } | null
  /** Shown above the search, e.g. why the candidate list was empty. */
  notice?: string | null
  /** Why the last pick was refused by the server. */
  linkError?: string | null
  busy?: boolean
}>()

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
  (e: 'pick', hit: CreditSearchHit, creditNoteHasPayer: boolean): void
}>()

const { t } = useI18n()
const { totpAwareFetch } = useFreshTotp()

const query = ref('')
const items = ref<CreditSearchHit[]>([])
const hasPayer = ref(true)
const loading = ref(false)
const error = ref<string | null>(null)
// Each keystroke starts a search; only the latest one may fill the list.
let seq = 0

async function search() {
  if (!props.creditNote) return
  const mine = ++seq
  loading.value = true
  error.value = null
  try {
    const params = new URLSearchParams({ any: '1' })
    if (query.value.trim()) params.set('q', query.value.trim())
    const res = await totpAwareFetch(
      `/api/v1/admin/financials/invoices/${props.creditNote.id}/credit-candidates?${params}`,
    )
    if (mine !== seq) return
    if (!res.ok) {
      error.value = `${res.status} ${await res.text().catch(() => '')}`
      items.value = []
      return
    }
    const body = await res.json()
    if (mine !== seq) return
    items.value = body.items ?? []
    hasPayer.value = body.credit_note_has_payer !== false
  } finally {
    if (mine === seq) loading.value = false
  }
}

watch(
  () => [props.open, props.creditNote?.id] as const,
  ([open]) => {
    if (!open) return
    query.value = ''
    items.value = []
    void search()
  },
  { immediate: true },
)
</script>

<template>
  <!-- z-40 keeps the cross-payer confirm dialog (z-50) above the search. -->
  <Modal
    :open="open"
    size="2xl"
    :z-index="40"
    :title="creditNote ? t('admin.faktura.credit.searchTitle', { number: creditNote.invoice_number }) : ''"
    @update:open="emit('update:open', $event)"
  >
    <p v-if="notice" class="rounded-md bg-amber-50 px-3 py-2 text-xs text-amber-800" data-testid="credit-search-notice">
      {{ notice }}
    </p>
    <p v-if="creditNote" class="mt-2 rounded-md bg-brand-50 px-3 py-2 text-xs text-brand-900">
      {{ t('admin.faktura.credit.searchBody', { amount: formatNOK(Math.abs(Number(creditNote.total_amount))) }) }}
    </p>
    <input
      v-model="query"
      type="search"
      :placeholder="t('admin.faktura.credit.searchPlaceholder')"
      class="mt-3 w-full rounded-md border border-gray-300 px-3 py-2 text-sm"
      data-testid="credit-search-input"
      @input="search"
    />
    <p v-if="linkError" class="mt-3 rounded-md bg-red-50 px-3 py-2 text-xs text-red-800" data-testid="credit-search-error">
      {{ linkError }}
    </p>
    <p v-if="error" class="mt-3 text-xs text-red-700">{{ error }}</p>
    <p v-else-if="loading && !items.length" class="mt-3 text-xs text-gray-500">{{ t('common.loading') }}…</p>
    <ul v-else-if="items.length" class="mt-3 max-h-96 divide-y divide-gray-100 overflow-y-auto">
      <li
        v-for="hit in items"
        :key="hit.id"
        class="flex items-center justify-between gap-3 py-2 text-sm"
        data-testid="credit-search-hit"
      >
        <div>
          <p class="font-semibold">
            #{{ hit.invoice_number }}
            <span class="font-normal text-gray-700">— {{ hit.payer_name || '—' }}</span>
            <span class="ml-1 text-sm font-semibold text-gray-800">{{ formatNOK(hit.total_amount) }}</span>
          </p>
          <p class="text-xs text-gray-500">
            {{ formatDate(hit.issue_date) }} ·
            {{ t(hit.settled ? 'admin.faktura.credit.statePaid' : 'admin.faktura.credit.stateOpen') }}
            <span v-if="!hit.same_amount" class="ml-1 rounded bg-red-100 px-1.5 py-0.5 font-semibold text-red-800">
              {{ t('admin.faktura.credit.otherAmount') }}
            </span>
            <span
              v-if="hasPayer && !hit.same_payer"
              class="ml-1 rounded bg-amber-100 px-1.5 py-0.5 font-semibold text-amber-800"
            >
              {{ t('admin.faktura.credit.otherPayer') }}
            </span>
          </p>
        </div>
        <!-- A different amount would be a partial credit, which the server refuses. -->
        <button
          type="button"
          class="rounded-md bg-brand-600 px-3 py-1 text-xs font-semibold text-white hover:bg-brand-700 disabled:cursor-not-allowed disabled:opacity-40"
          :disabled="busy || !hit.same_amount"
          :title="hit.same_amount ? undefined : t('admin.faktura.credit.error.partial_credit')"
          data-testid="credit-search-pick"
          @click="emit('pick', hit, hasPayer)"
        >
          {{ t('admin.faktura.credit.pick') }}
        </button>
      </li>
    </ul>
    <p v-else class="mt-3 text-xs text-gray-500">{{ t('admin.faktura.credit.searchEmpty') }}</p>
  </Modal>
</template>
