<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAllDugnadHours, useSetRequiredHours } from '@/composables/useDugnad'
import { Settings } from 'lucide-vue-next'

const { t } = useI18n()
const { data: allHours, isLoading } = useAllDugnadHours()
const setRequired = useSetRequiredHours()

const showSettings = ref(false)
const requiredInput = ref('')
const toast = ref<{ type: 'success' | 'error'; message: string } | null>(null)

function showToast(type: 'success' | 'error', message: string) {
  toast.value = { type, message }
  setTimeout(() => (toast.value = null), 3000)
}

function openSettings() {
  requiredInput.value = allHours.value?.[0]?.required_hours?.toString() ?? '0'
  showSettings.value = true
}

function handleSetRequired() {
  const hours = Number(requiredInput.value)
  if (isNaN(hours) || hours < 0) return
  setRequired.mutate(hours, {
    onSuccess: () => {
      showSettings.value = false
      showToast('success', t('dugnad.settingsUpdated'))
    },
  })
}
</script>

<template>
  <div>
    <div class="flex items-center justify-between">
      <h1 class="text-2xl font-bold text-gray-900">{{ t('dugnad.title') }} — {{ t('dugnad.allMembers') }}</h1>
      <button
        class="flex items-center gap-1.5 rounded-md border border-gray-300 px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
        @click="openSettings"
      >
        <Settings class="h-4 w-4" />
        {{ t('dugnad.settings') }}
      </button>
    </div>

    <div
      v-if="toast"
      :class="['mt-4 rounded-md p-3 text-sm', toast.type === 'success' ? 'bg-green-50 text-green-800' : 'bg-red-50 text-red-800']"
    >
      {{ toast.message }}
    </div>

    <div v-if="isLoading" class="mt-6 text-gray-500">{{ t('common.loading') }}...</div>

    <div v-else class="mt-6 overflow-x-auto rounded-lg border border-gray-200">
      <table class="min-w-full divide-y divide-gray-200">
        <thead class="bg-gray-50">
          <tr>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase text-gray-500">{{ t('common.name') }}</th>
            <th scope="col" class="px-4 py-3 text-right text-xs font-medium uppercase text-gray-500">{{ t('dugnad.signedUpHours') }}</th>
            <th scope="col" class="px-4 py-3 text-right text-xs font-medium uppercase text-gray-500">{{ t('dugnad.completedHours') }}</th>
            <th scope="col" class="px-4 py-3 text-right text-xs font-medium uppercase text-gray-500">{{ t('dugnad.requiredHours') }}</th>
            <th scope="col" class="px-4 py-3 text-right text-xs font-medium uppercase text-gray-500">{{ t('dugnad.remaining') }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 bg-white">
          <tr v-for="member in allHours" :key="member.user_id">
            <td class="whitespace-nowrap px-4 py-3 text-sm font-medium text-gray-900">{{ member.name }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-right text-sm text-gray-500">{{ member.signed_up_hours }}{{ t('dugnad.hoursUnit') }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-right text-sm text-gray-500">{{ member.completed_hours }}{{ t('dugnad.hoursUnit') }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-right text-sm text-gray-500">{{ member.required_hours }}{{ t('dugnad.hoursUnit') }}</td>
            <td :class="['whitespace-nowrap px-4 py-3 text-right text-sm font-medium', member.remaining > 0 ? 'text-orange-600' : 'text-green-600']">
              {{ member.remaining > 0 ? member.remaining : Math.abs(member.remaining) }}{{ t('dugnad.hoursUnit') }}
              {{ member.remaining > 0 ? '' : '✓' }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Settings modal -->
    <div
      v-if="showSettings"
      role="dialog" aria-modal="true" class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      @click.self="showSettings = false"
    >
      <div class="w-full max-w-sm rounded-lg bg-white p-6 shadow-xl">
        <h2 class="text-lg font-semibold text-gray-900">{{ t('dugnad.settings') }}</h2>
        <form class="mt-4 space-y-4" @submit.prevent="handleSetRequired">
          <div>
            <label class="block text-sm font-medium text-gray-700">{{ t('dugnad.requiredHoursPerYear') }}</label>
            <input
              v-model="requiredInput"
              type="number"
              min="0"
              step="1"
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>
          <div class="flex justify-end gap-3">
            <button
              type="button"
              class="rounded-md border border-gray-300 px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
              @click="showSettings = false"
            >
              {{ t('common.cancel') }}
            </button>
            <button
              type="submit"
              :disabled="setRequired.isPending.value"
              class="rounded-md bg-blue-600 px-3 py-2 text-sm font-semibold text-white shadow-sm hover:bg-blue-700 disabled:opacity-50"
            >
              {{ t('common.save') }}
            </button>
          </div>
        </form>
      </div>
    </div>
  </div>
</template>
