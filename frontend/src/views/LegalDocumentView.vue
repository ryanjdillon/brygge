<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Loader2 } from 'lucide-vue-next'
import { ApiError, useApiClient, unwrap } from '@/lib/apiClient'
import { formatDate } from '@/lib/format'
import { JOIN_LEGAL_DOCS, type JoinLegalDoc } from '@/lib/joinFlow'
import BuiltInSalesTerms from '@/components/legal/BuiltInSalesTerms.vue'

// The public reading page for one of the club's published legal documents:
// /vilkar/:docType (linked from the join form), /privacy, /salgsvilkar and the
// portal's privacy policy all render through here. The terms of sale fall
// back to the built-in text, so the page Vipps links to always resolves.

const props = defineProps<{
  docType: string
  /** Inside a layout that already pads its content, such as the portal. */
  embedded?: boolean
}>()

const { t, locale } = useI18n()
const client = useApiClient()

type Doc = { version: string; content: string; published_at: string }

const REQUEST_TIMEOUT_MS = 5000

const docType = computed<JoinLegalDoc | null>(() =>
  (JOIN_LEGAL_DOCS as readonly string[]).includes(props.docType) ? (props.docType as JoinLegalDoc) : null,
)
// missing: nothing published (404, or a published row with no text);
// error: the request failed or timed out, so we cannot tell.
type LoadState = 'loading' | 'ready' | 'missing' | 'error'
const state = ref<LoadState>('loading')
const doc = ref<Doc | null>(null)

const showBuiltInTerms = computed(
  () => docType.value === 'terms_of_sale' && (state.value === 'missing' || state.value === 'error'),
)

// Only the latest request may set the state, so a slow answer for a
// previous document type cannot overwrite the current one.
let requestSeq = 0

async function load() {
  const seq = ++requestSeq
  doc.value = null
  if (!docType.value) {
    state.value = 'missing'
    return
  }
  state.value = 'loading'
  let next: LoadState
  let loaded: Doc | null = null
  try {
    const result = unwrap(
      await client.GET('/api/v1/legal/{docType}', {
        params: { path: { docType: docType.value } },
        signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
      }),
    )
    if (result && typeof result.content === 'string' && result.content.trim() !== '') {
      loaded = result
      next = 'ready'
    } else {
      next = 'missing'
    }
  } catch (err) {
    next = err instanceof ApiError && err.status === 404 ? 'missing' : 'error'
  }
  if (seq !== requestSeq) return
  doc.value = loaded
  state.value = next
}

watch(docType, load, { immediate: true })
</script>

<template>
  <BuiltInSalesTerms v-if="showBuiltInTerms" />
  <article v-else :class="embedded ? 'mx-auto max-w-3xl' : 'mx-auto max-w-3xl px-4 py-8 sm:px-6 sm:py-10'">
    <h1 class="text-2xl font-bold text-gray-900 sm:text-3xl">
      {{ docType ? t(`join.legal.${docType}`) : t('join.legal.notFound') }}
    </h1>
    <div v-if="state === 'loading'" class="py-12 text-center" role="status">
      <Loader2 class="mx-auto h-8 w-8 animate-spin text-gray-400" aria-hidden="true" />
      <span class="sr-only">{{ t('common.loading') }}</span>
    </div>
    <p v-else-if="state === 'error'" class="mt-6 rounded-md bg-red-50 p-4 text-sm text-red-800" role="alert" data-testid="legal-error">
      {{ t('join.legal.loadFailed') }}
    </p>
    <p v-else-if="state === 'missing' || !doc" class="mt-6 rounded-md bg-amber-50 p-4 text-sm text-amber-800" data-testid="legal-missing">
      {{ t('join.legal.notPublished') }}
    </p>
    <div v-else class="mt-6 rounded-lg border border-gray-200 bg-white p-4 sm:p-6">
      <p class="mb-4 text-xs text-gray-500">
        {{ t('join.legal.version', { version: doc.version, date: formatDate(doc.published_at, locale) }) }}
      </p>
      <div class="max-w-none whitespace-pre-wrap break-words text-sm text-gray-800" data-testid="legal-content">{{ doc.content }}</div>
    </div>
  </article>
</template>
