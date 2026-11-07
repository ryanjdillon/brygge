<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import { Loader2 } from 'lucide-vue-next'
import { useApiClient, unwrap } from '@/lib/apiClient'
import { formatDate } from '@/lib/format'
import { JOIN_LEGAL_DOCS, type JoinLegalDoc } from '@/lib/joinFlow'

// The public reading page for one of the club's published legal documents,
// linked from the join form so a visitor can read what they consent to
// before they have an account.

const { t, locale } = useI18n()
const route = useRoute()
const client = useApiClient()

type Doc = { version: string; content: string; published_at: string }

const docType = computed<JoinLegalDoc | null>(() => {
  const p = route.params.docType
  return typeof p === 'string' && (JOIN_LEGAL_DOCS as readonly string[]).includes(p) ? (p as JoinLegalDoc) : null
})
const state = ref<'loading' | 'ready' | 'missing'>('loading')
const doc = ref<Doc | null>(null)

async function load() {
  doc.value = null
  if (!docType.value) {
    state.value = 'missing'
    return
  }
  state.value = 'loading'
  try {
    doc.value = unwrap(await client.GET('/api/v1/legal/{docType}', { params: { path: { docType: docType.value } } }))
    state.value = 'ready'
  } catch {
    state.value = 'missing'
  }
}

watch(docType, load, { immediate: true })
</script>

<template>
  <article class="mx-auto max-w-3xl px-4 py-8 sm:px-6 sm:py-10">
    <h1 class="text-2xl font-bold text-gray-900 sm:text-3xl">
      {{ docType ? t(`join.legal.${docType}`) : t('join.legal.notFound') }}
    </h1>
    <div v-if="state === 'loading'" class="py-12 text-center" role="status">
      <Loader2 class="mx-auto h-8 w-8 animate-spin text-gray-400" aria-hidden="true" />
      <span class="sr-only">{{ t('common.loading') }}</span>
    </div>
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
