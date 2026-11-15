<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useNotificationConfig, useUpdateNotificationConfig, useTestPush } from '@/composables/useNotifications'
import NumberInput from '@/components/ui/form/NumberInput.vue'
import TestPushNotice from '@/components/notifications/TestPushNotice.vue'
import { testPushFailure, testPushOutcome, type TestPushMessage } from '@/lib/testPushResult'

const { t } = useI18n()
const { categories, isLoading } = useNotificationConfig()
const { mutate: updateConfig } = useUpdateNotificationConfig()
const { mutate: sendTest, isPending: testSending } = useTestPush()

// Same outcome text as the member's page: 0 sent, 409 and 503 explained.
const testResult = ref<TestPushMessage | null>(null)

function toggleRequired(category: string, currentRequired: boolean) {
  updateConfig({ category, required: !currentRequired })
}

function updateLeadDays(category: string, value: number | null) {
  updateConfig({ category, required: categories.value.find((c) => c.category === category)?.required ?? false, lead_days: value ?? 0 })
}

function handleTestPush() {
  testResult.value = null
  sendTest(undefined, {
    onSuccess: (res) => {
      testResult.value = testPushOutcome(t, res)
    },
    onError: (err) => {
      testResult.value = testPushFailure(t, err)
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
        type="button"
        :disabled="testSending"
        data-testid="admin-send-test-push"
        class="min-h-11 rounded-md bg-brand-600 px-4 py-2 text-sm font-semibold text-white hover:bg-brand-700 disabled:opacity-50"
        @click="handleTestPush"
      >
        {{ testSending ? t('common.loading') : t('notifications.admin.testPush') }}
      </button>
    </div>

    <TestPushNotice v-if="testResult" :message="testResult" />

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
