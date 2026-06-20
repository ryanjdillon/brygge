<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import Switch from '@/components/ui/form/Switch.vue'
import { useFreshTotp } from '@/composables/useFreshTotp'
import { useFeatures } from '@/composables/useFeatures'

const { t } = useI18n()
const queryClient = useQueryClient()
const { ensureFreshTotp } = useFreshTotp()
const { features } = useFeatures()

type FeedbackSettings = {
  enabled: boolean
  has_api_key: boolean
  linear_team_id: string
  linear_triage_state_id: string
}

const { data: settings, isLoading } = useQuery({
  queryKey: ['admin-feedback-settings'],
  queryFn: async () => {
    const res = await fetch('/api/v1/admin/settings/feedback', { credentials: 'include' })
    if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
    return res.json() as Promise<FeedbackSettings>
  },
})

const enabled = ref(false)
const apiKey = ref('')
const teamID = ref('')
const triageStateID = ref('')
const hasExistingKey = ref(false)

watch(settings, (s) => {
  if (!s) return
  enabled.value = s.enabled
  hasExistingKey.value = s.has_api_key
  teamID.value = s.linear_team_id
  triageStateID.value = s.linear_triage_state_id
}, { immediate: true })

const apiKeyPlaceholder = computed(() =>
  hasExistingKey.value ? t('admin.feedbackSettings.apiKeySet') : t('admin.feedbackSettings.apiKeyPlaceholder'),
)

const apiKeyFormatError = computed(() => {
  const k = apiKey.value.trim()
  if (!k) return null
  return k.startsWith('lin_api_') ? null : t('admin.feedbackSettings.apiKeyInvalid')
})

const canSave = computed(() => {
  if (apiKeyFormatError.value) return false
  if (!enabled.value) return true
  const keyOk = apiKey.value.trim() !== '' || hasExistingKey.value
  return keyOk && teamID.value.trim() !== '' && triageStateID.value.trim() !== ''
})

const saved = ref(false)
const saveError = ref<string | null>(null)

const { mutateAsync: save, isPending: saving } = useMutation({
  mutationFn: async () => {
    const body: Record<string, unknown> = {
      enabled: enabled.value,
      linear_team_id: teamID.value.trim(),
      linear_triage_state_id: triageStateID.value.trim(),
    }
    if (apiKey.value.trim()) {
      body.linear_api_key = apiKey.value.trim()
    }
    const res = await fetch('/api/v1/admin/settings/feedback', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify(body),
    })
    if (!res.ok) {
      const err = await res.json().catch(() => null)
      throw new Error(err?.error ?? `${res.status} ${res.statusText}`)
    }
    return res.json()
  },
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['admin-feedback-settings'] })
    queryClient.invalidateQueries({ queryKey: ['features'] })
    apiKey.value = ''
    saved.value = true
    saveError.value = null
    setTimeout(() => (saved.value = false), 3000)
  },
  onError: (err: unknown) => {
    saveError.value = err instanceof Error ? err.message : String(err)
  },
})

async function handleSubmit() {
  if (!(await ensureFreshTotp())) return
  await save()
}
</script>

<template>
  <div class="space-y-6">
    <div>
      <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.feedbackSettings.title') }}</h1>
      <p class="mt-1 text-sm text-gray-500">{{ t('admin.feedbackSettings.subtitle') }}</p>
    </div>

    <div v-if="isLoading" class="animate-pulse space-y-4">
      <div class="h-12 rounded bg-gray-100" />
      <div class="h-12 rounded bg-gray-100" />
    </div>

    <form v-else class="max-w-lg space-y-6" @submit.prevent="handleSubmit">
      <!-- Enable toggle -->
      <div class="rounded-lg border border-gray-200 bg-white p-5">
        <div class="flex items-start justify-between gap-4">
          <div>
            <p class="text-sm font-medium text-gray-900">{{ t('admin.feedbackSettings.enableLabel') }}</p>
            <p class="mt-0.5 text-xs text-gray-500">{{ t('admin.feedbackSettings.enableHelp') }}</p>
          </div>
          <Switch v-model="enabled" />
        </div>
      </div>

      <!-- Linear credentials — only shown when enabling -->
      <div v-if="enabled || features.feedback" class="rounded-lg border border-gray-200 bg-white p-5 space-y-5">
        <h2 class="text-sm font-semibold uppercase tracking-wider text-gray-500">
          {{ t('admin.feedbackSettings.linearSection') }}
        </h2>

        <div>
          <label for="fb-api-key" class="block text-sm font-medium text-gray-700">
            {{ t('admin.feedbackSettings.apiKeyLabel') }}
          </label>
          <input
            id="fb-api-key"
            v-model="apiKey"
            type="password"
            autocomplete="new-password"
            class="mt-1 w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            :placeholder="apiKeyPlaceholder"
          />
          <p v-if="apiKeyFormatError" class="mt-1 text-xs text-red-600">{{ apiKeyFormatError }}</p>
          <p v-else class="mt-1 text-xs text-gray-500">{{ t('admin.feedbackSettings.apiKeyHelp') }}</p>
        </div>

        <div>
          <label for="fb-team-id" class="block text-sm font-medium text-gray-700">
            {{ t('admin.feedbackSettings.teamIdLabel') }}
          </label>
          <input
            id="fb-team-id"
            v-model="teamID"
            type="text"
            class="mt-1 w-full rounded-md border border-gray-300 px-3 py-2 font-mono text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            :placeholder="t('admin.feedbackSettings.teamIdPlaceholder')"
          />
          <p class="mt-1 text-xs text-gray-500">{{ t('admin.feedbackSettings.teamIdHelp') }}</p>
        </div>

        <div>
          <label for="fb-triage-id" class="block text-sm font-medium text-gray-700">
            {{ t('admin.feedbackSettings.triageIdLabel') }}
          </label>
          <input
            id="fb-triage-id"
            v-model="triageStateID"
            type="text"
            class="mt-1 w-full rounded-md border border-gray-300 px-3 py-2 font-mono text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            :placeholder="t('admin.feedbackSettings.triageIdPlaceholder')"
          />
          <p class="mt-1 text-xs text-gray-500">{{ t('admin.feedbackSettings.triageIdHelp') }}</p>
        </div>

        <div v-if="enabled && !canSave" class="rounded-md bg-amber-50 border border-amber-200 p-3 text-xs text-amber-800">
          {{ t('admin.feedbackSettings.incompleteWarning') }}
        </div>
      </div>

      <div v-if="saveError" class="rounded-md bg-red-50 p-3 text-sm text-red-800">{{ saveError }}</div>

      <div class="flex items-center gap-3">
        <button
          type="submit"
          :disabled="saving || !canSave"
          class="rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white hover:bg-blue-700 disabled:opacity-50"
        >
          {{ saving ? t('common.loading') : t('common.save') }}
        </button>
        <span v-if="saved" class="text-sm text-green-600">{{ t('common.success') }}</span>
      </div>
    </form>
  </div>
</template>
