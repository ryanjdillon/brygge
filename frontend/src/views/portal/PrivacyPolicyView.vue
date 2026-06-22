<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { ShieldCheck } from 'lucide-vue-next'
import { useLegalDocument } from '@/composables/useGdpr'
import { formatDate } from '@/lib/format'

const { t } = useI18n()
const { data: doc, isLoading, isError } = useLegalDocument('privacy')

const publishedAt = computed(() => {
  const v = doc.value?.published_at
  return formatDate(v)
})
</script>

<template>
  <div class="mx-auto max-w-3xl space-y-4">
    <div class="flex items-center gap-3">
      <ShieldCheck class="h-6 w-6 text-brand-600" />
      <h1 class="text-2xl font-bold text-gray-900">{{ t('privacyPolicy.title') }}</h1>
    </div>

    <div v-if="isLoading" class="text-sm text-gray-500">{{ t('common.loading') }}...</div>

    <div v-else-if="isError || !doc" class="rounded-md bg-amber-50 p-4 text-sm text-amber-800">
      {{ t('privacyPolicy.notPublished') }}
    </div>

    <article v-else class="rounded-lg border border-gray-200 bg-white p-6">
      <p class="mb-4 text-xs text-gray-500">
        {{ t('privacyPolicy.version', { version: doc.version, date: publishedAt }) }}
      </p>
      <div class="prose prose-sm max-w-none whitespace-pre-wrap text-gray-800">{{ doc.content }}</div>
    </article>
  </div>
</template>
