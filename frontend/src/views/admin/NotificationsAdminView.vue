<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useNotificationConfig, useUpdateNotificationConfig, useTestPush } from '@/composables/useNotifications'
import NumberInput from '@/components/ui/form/NumberInput.vue'

const { t } = useI18n()
const { categories, isLoading } = useNotificationConfig()
const { mutate: updateConfig } = useUpdateNotificationConfig()
const { mutate: sendTest, isPending: testSending } = useTestPush()

const toast = ref<{ type: 'success' | 'error'; message: string } | null>(null)

function toggleRequired(category: string, currentRequired: boolean) {
  updateConfig({ category, required: !currentRequired })
}

function updateLeadDays(category: string, value: number | null) {
  updateConfig({ category, required: categories.value.find((c) => c.category === category)?.required ?? false, lead_days: value ?? 0 })
}

function handleTestPush() {
  sendTest(undefined, {
    onSuccess: () => {
      toast.value = { type: 'success', message: t('notifications.admin.testSent') }
      setTimeout(() => (toast.value = null), 3000)
    },
    onError: () => {
      toast.value = { type: 'error', message: t('notifications.admin.testFailed') }
      setTimeout(() => (toast.value = null), 3000)
    },
  })
}
</script>

<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between">
      <div>
        <h1 class="text-2xl font-bold text-gray-900">{{ t('notifications.admin.title') }}</h1>
        <p class="mt-1 text-sm text-gray-600">{{ t('notifications.admin.subtitle') }}</p>
      </div>
      <button
        :disabled="testSending"
        class="rounded-md bg-brand-600 px-4 py-2 text-sm font-semibold text-white hover:bg-brand-700 disabled:opacity-50"
        @click="handleTestPush"
      >
        {{ testSending ? t('common.loading') : t('notifications.admin.testPush') }}
      </button>
    </div>

    <div
      v-if="toast"
      :class="[
        'rounded-md p-3 text-sm',
        toast.type === 'success' ? 'bg-green-50 text-green-800' : 'bg-red-50 text-red-800',
      ]"
    >
      {{ toast.message }}
    </div>

    <div v-if="isLoading" class="animate-pulse space-y-4">
      <div v-for="i in 6" :key="i" class="h-12 rounded bg-gray-100" />
    </div>

    <div v-else class="overflow-hidden rounded-lg border border-gray-200 bg-white">
      <table class="min-w-full divide-y divide-gray-200">
        <thead class="bg-gray-50">
          <tr>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('admin.pricing.category') }}
            </th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('notifications.admin.requiredToggle') }}
            </th>
            <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('notifications.admin.leadDays') }}
            </th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-100">
          <tr v-for="cat in categories" :key="cat.category">
            <td class="px-4 py-3 text-sm font-medium text-gray-900">
              {{ t(`notifications.cat.${cat.category}`) }}
            </td>
            <td class="px-4 py-3">
              <button
                class="relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 focus:outline-none focus:ring-2 focus:ring-brand-500 focus:ring-offset-2"
                :class="cat.required ? 'bg-brand-600' : 'bg-gray-200'"
                role="switch"
                :aria-checked="cat.required"
                @click="toggleRequired(cat.category, cat.required)"
              >
                <span
                  class="pointer-events-none inline-block h-5 w-5 rounded-full bg-white shadow ring-0 transition-transform duration-200"
                  :class="cat.required ? 'translate-x-5' : 'translate-x-0'"
                />
              </button>
            </td>
            <td class="px-4 py-3">
              <div v-if="cat.lead_days !== null" class="w-20">
                <NumberInput
                  :model-value="cat.lead_days"
                  :min="0"
                  :max="30"
                  @change="(v) => updateLeadDays(cat.category, v)"
                />
              </div>
              <span v-else class="text-sm text-gray-400">--</span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
