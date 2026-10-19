<script setup lang="ts">
import { ref, watch, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import Modal from '@/components/ui/Modal.vue'
import { useFreshTotp } from '@/composables/useFreshTotp'
import { formatNOK, formatDate } from '@/lib/format'

// Manual search for the faktura a credit note settles (BRY-393), for when
// the same-payer, same-amount candidates come up empty, e.g. an imported
// credit note without a member. The server still refuses a partial credit
// and asks for confirmation across payers; this list only shows the
// mismatches up front.

// One row of GET …/credit-candidates, for both the same-payer candidates
// and the manual search.
export interface CreditSearchHit {
  id: string
  invoice_number: number
  total_amount: number
  issue_date: string
  paid: boolean
  /** Settled for the member: linking makes the credit note credit owed. */
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
const truncated = ref(false)
const loading = ref(false)
const error = ref<string | null>(null)
// Searches overlap while the treasurer types; only the latest may fill the list.
let seq = 0
let debounce: ReturnType<typeof setTimeout> | undefined

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
    const body = await res.json().catch(() => null)
    if (mine !== seq) return
    if (!res.ok) {
      error.value = body?.code
        ? t(`admin.faktura.credit.error.${body.code}`, body?.error ?? '')
        : `${res.status} ${body?.error ?? ''}`
      items.value = []
      truncated.value = false
      return
    }
    items.value = body?.items ?? []
    truncated.value = body?.truncated === true
    hasPayer.value = body?.credit_note_has_payer !== false
  } catch (e) {
    // A network failure or an abandoned step-up must not leave the list
    // silently stale.
    if (mine !== seq) return
    error.value = e instanceof Error ? e.message : String(e)
    items.value = []
    truncated.value = false
  } finally {
    if (mine === seq) loading.value = false
  }
}

function onInput() {
  if (debounce) clearTimeout(debounce)
  debounce = setTimeout(() => void search(), 200)
}

onBeforeUnmount(() => {
  if (debounce) clearTimeout(debounce)
})

watch(
  () => [props.open, props.creditNote?.id] as const,
  ([open]) => {
    if (debounce) clearTimeout(debounce)
    if (!open) return
    query.value = ''
    items.value = []
    truncated.value = false
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
      @input="onInput"
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
    <p v-if="truncated && items.length" class="mt-2 text-xs text-gray-500" data-testid="credit-search-truncated">
      {{ t('admin.faktura.credit.searchTruncated', { count: items.length }) }}
    </p>
  </Modal>
</template>
