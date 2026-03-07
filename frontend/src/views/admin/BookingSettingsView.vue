<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'

const { t } = useI18n()
const { fetchApi } = useApi()
const queryClient = useQueryClient()

const { data: settings, isLoading } = useQuery({
  queryKey: ['admin-booking-settings'],
  queryFn: () => fetchApi<Record<string, number | string>>('/api/v1/admin/settings/booking'),
})

const form = ref({
  hoist_slot_duration_minutes: 120,
  hoist_open_hour: 8,
  hoist_close_hour: 20,
  hoist_max_consecutive_slots: 2,
  slip_share_rebate_pct: 25,
})

watch(settings, (s) => {
  if (!s) return
  form.value.hoist_slot_duration_minutes = Number(s.hoist_slot_duration_minutes) || 120
  form.value.hoist_open_hour = Number(s.hoist_open_hour) || 8
  form.value.hoist_close_hour = Number(s.hoist_close_hour) || 20
  form.value.hoist_max_consecutive_slots = Number(s.hoist_max_consecutive_slots) || 2
  form.value.slip_share_rebate_pct = Number(s.slip_share_rebate_pct) || 25
}, { immediate: true })

const saved = ref(false)

const { mutateAsync: save, isPending: saving } = useMutation({
  mutationFn: () =>
    fetchApi('/api/v1/admin/settings/booking', {
      method: 'PUT',
      body: JSON.stringify({
        settings: Object.fromEntries(
          Object.entries(form.value).map(([k, v]) => [k, v]),
        ),
      }),
    }),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['admin-booking-settings'] })
    saved.value = true
    setTimeout(() => (saved.value = false), 3000)
  },
})
</script>

<template>
  <div class="space-y-6">
    <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.bookingSettings.title') }}</h1>

    <div v-if="isLoading" class="animate-pulse space-y-4">
      <div v-for="i in 5" :key="i" class="h-12 rounded bg-gray-100" />
    </div>

    <form v-else class="max-w-lg space-y-5" @submit.prevent="save()">
      <fieldset class="space-y-4">
        <legend class="text-lg font-semibold text-gray-900">{{ t('admin.bookingSettings.hoist') }}</legend>

        <div>
          <label class="block text-sm font-medium text-gray-700">{{ t('admin.bookingSettings.slotDuration') }}</label>
          <input v-model.number="form.hoist_slot_duration_minutes" type="number" min="30" step="30"
            class="mt-1 block w-full rounded-md border-gray-300 text-sm" />
        </div>

        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="block text-sm font-medium text-gray-700">{{ t('admin.bookingSettings.openHour') }}</label>
            <input v-model.number="form.hoist_open_hour" type="number" min="0" max="23"
              class="mt-1 block w-full rounded-md border-gray-300 text-sm" />
          </div>
          <div>
            <label class="block text-sm font-medium text-gray-700">{{ t('admin.bookingSettings.closeHour') }}</label>
            <input v-model.number="form.hoist_close_hour" type="number" min="1" max="24"
              class="mt-1 block w-full rounded-md border-gray-300 text-sm" />
          </div>
        </div>

        <div>
          <label class="block text-sm font-medium text-gray-700">{{ t('admin.bookingSettings.maxConsecutiveSlots') }}</label>
          <input v-model.number="form.hoist_max_consecutive_slots" type="number" min="1" max="10"
            class="mt-1 block w-full rounded-md border-gray-300 text-sm" />
        </div>
      </fieldset>

      <fieldset class="space-y-4">
        <legend class="text-lg font-semibold text-gray-900">{{ t('admin.bookingSettings.slipSharing') }}</legend>

        <div>
          <label class="block text-sm font-medium text-gray-700">{{ t('admin.bookingSettings.rebatePct') }}</label>
          <input v-model.number="form.slip_share_rebate_pct" type="number" min="0" max="100"
            class="mt-1 block w-full rounded-md border-gray-300 text-sm" />
        </div>
      </fieldset>

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
