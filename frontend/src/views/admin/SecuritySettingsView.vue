<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'
import { useFreshTotp } from '@/composables/useFreshTotp'

const { t } = useI18n()
const client = useApiClient() as any
const queryClient = useQueryClient()
const { ensureFreshTotp } = useFreshTotp()

type SessionSettings = {
  idle_minutes: number
  cap_minutes: number
  admin_totp_minutes: number
}

const { data: settings, isLoading } = useQuery({
  queryKey: ['admin-security-settings'],
  queryFn: async () => unwrap(await client.GET('/api/v1/admin/settings/security')) as SessionSettings,
})

const form = ref<SessionSettings>({ idle_minutes: 720, cap_minutes: 10080, admin_totp_minutes: 720 })

watch(settings, (s) => {
  if (s) form.value = { ...s }
}, { immediate: true })

const saved = ref(false)
const saveError = ref<string | null>(null)

const { mutateAsync: save, isPending: saving } = useMutation({
  mutationFn: async () =>
    unwrap(await client.PUT('/api/v1/admin/settings/security', { body: form.value })),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['admin-security-settings'] })
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

function idleHours() { return Math.round(form.value.idle_minutes / 60 * 10) / 10 }
function capDays() { return Math.round(form.value.cap_minutes / 1440 * 10) / 10 }
function totpHours() { return Math.round(form.value.admin_totp_minutes / 60 * 10) / 10 }
</script>

<template>
  <div class="space-y-6">
    <div>
      <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.securitySettings.title') }}</h1>
      <p class="mt-1 text-sm text-gray-500">{{ t('admin.securitySettings.subtitle') }}</p>
    </div>

    <div v-if="isLoading" class="animate-pulse space-y-4">
      <div class="h-12 rounded bg-gray-100" />
      <div class="h-12 rounded bg-gray-100" />
      <div class="h-12 rounded bg-gray-100" />
    </div>

    <form v-else class="max-w-lg space-y-6" @submit.prevent="handleSubmit">
      <div class="rounded-lg border border-gray-200 bg-white p-5 space-y-5">
        <h2 class="text-sm font-semibold uppercase tracking-wider text-gray-500">
          {{ t('admin.securitySettings.sessionSection') }}
        </h2>

        <div>
          <label for="idle-minutes" class="block text-sm font-medium text-gray-700">
            {{ t('admin.securitySettings.idleWindow') }}
          </label>
          <div class="mt-1 flex items-center gap-3">
            <input
              id="idle-minutes"
              v-model.number="form.idle_minutes"
              type="number"
              min="30"
              max="43200"
              step="30"
              class="w-28 rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
            <span class="text-sm text-gray-500">{{ t('admin.securitySettings.minutes') }}</span>
            <span class="text-xs text-gray-400">({{ idleHours() }}h)</span>
          </div>
          <p class="mt-1 text-xs text-gray-500">{{ t('admin.securitySettings.idleWindowHelp') }}</p>
        </div>

        <div>
          <label for="cap-minutes" class="block text-sm font-medium text-gray-700">
            {{ t('admin.securitySettings.absoluteCap') }}
          </label>
          <div class="mt-1 flex items-center gap-3">
            <input
              id="cap-minutes"
              v-model.number="form.cap_minutes"
              type="number"
              :min="form.idle_minutes"
              max="129600"
              step="60"
              class="w-28 rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
            <span class="text-sm text-gray-500">{{ t('admin.securitySettings.minutes') }}</span>
            <span class="text-xs text-gray-400">({{ capDays() }}d)</span>
          </div>
          <p class="mt-1 text-xs text-gray-500">{{ t('admin.securitySettings.absoluteCapHelp') }}</p>
        </div>
      </div>

      <div class="rounded-lg border border-gray-200 bg-white p-5 space-y-5">
        <h2 class="text-sm font-semibold uppercase tracking-wider text-gray-500">
          {{ t('admin.securitySettings.totpSection') }}
        </h2>

        <div>
          <label for="admin-totp-minutes" class="block text-sm font-medium text-gray-700">
            {{ t('admin.securitySettings.adminTotpWindow') }}
          </label>
          <div class="mt-1 flex items-center gap-3">
            <input
              id="admin-totp-minutes"
              v-model.number="form.admin_totp_minutes"
              type="number"
              min="5"
              max="1440"
              step="5"
              class="w-28 rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
            <span class="text-sm text-gray-500">{{ t('admin.securitySettings.minutes') }}</span>
            <span class="text-xs text-gray-400">({{ totpHours() }}h)</span>
          </div>
          <p class="mt-1 text-xs text-gray-500">{{ t('admin.securitySettings.adminTotpWindowHelp') }}</p>
        </div>
      </div>

      <div class="rounded-md bg-amber-50 border border-amber-200 p-3 text-xs text-amber-800">
        {{ t('admin.securitySettings.newSessionsNote') }}
      </div>

      <div v-if="saveError" class="rounded-md bg-red-50 p-3 text-sm text-red-800">{{ saveError }}</div>

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
