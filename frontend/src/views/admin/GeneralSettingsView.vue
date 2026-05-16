<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'
import { LOCALE_OPTIONS } from '@/i18n'

const { t } = useI18n()
// The general-settings endpoint isn't in the generated OpenAPI types
// yet (DIL-327 tracks registering the remaining endpoints); use an
// untyped client for these two calls.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const client = useApiClient() as any
const queryClient = useQueryClient()
const localeOptions = LOCALE_OPTIONS

const { data: settings, isLoading } = useQuery({
  queryKey: ['admin-general-settings'],
  queryFn: async () => unwrap(await client.GET('/api/v1/admin/settings/general')),
})

const form = ref<{ default_language: string }>({ default_language: 'nb' })

watch(
  settings,
  (s) => {
    const v = (s as { default_language?: string } | undefined)?.default_language
    if (v) form.value.default_language = v
  },
  { immediate: true },
)

const saved = ref(false)

const { mutateAsync: save, isPending: saving } = useMutation({
  mutationFn: async () =>
    unwrap(
      await client.PUT('/api/v1/admin/settings/general', {
        body: { default_language: form.value.default_language },
      }),
    ),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['admin-general-settings'] })
    saved.value = true
    setTimeout(() => (saved.value = false), 3000)
  },
})
</script>

<template>
  <div class="space-y-6">
    <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.generalSettings.title') }}</h1>

    <div v-if="isLoading" class="animate-pulse space-y-4">
      <div class="h-12 rounded bg-gray-100" />
    </div>

    <form v-else class="max-w-lg space-y-5" @submit.prevent="save()">
      <div>
        <label for="club-default-language" class="block text-sm font-medium text-gray-700">
          {{ t('admin.generalSettings.defaultLanguage') }}
        </label>
        <select
          id="club-default-language"
          v-model="form.default_language"
          class="mt-1 w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        >
          <option v-for="opt in localeOptions" :key="opt.code" :value="opt.code">
            {{ opt.label }}
          </option>
        </select>
        <p class="mt-1 text-xs text-gray-500">{{ t('admin.generalSettings.defaultLanguageHelp') }}</p>
      </div>

      <div class="flex items-center gap-3">
        <button
          type="submit"
          :disabled="saving"
          class="rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white hover:bg-blue-700 disabled:opacity-50"
        >
          {{ saving ? t('common.loading') : t('common.save') }}
        </button>
        <span v-if="saved" class="text-sm text-green-600">{{ t('common.success') }}</span>
      </div>
    </form>
  </div>
</template>
