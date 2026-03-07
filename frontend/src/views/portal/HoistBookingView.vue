<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Wrench } from 'lucide-vue-next'
import { useHoistSlots, useCreateBooking } from '@/composables/useBookings'

const { t } = useI18n()

const today = new Date().toISOString().slice(0, 10)
const selectedDate = ref(today)
const selectedSlots = ref<number[]>([])
const errorMsg = ref('')

const dateRef = computed(() => selectedDate.value)
const { data: slotsData, isLoading } = useHoistSlots(dateRef)
const { mutateAsync: createBooking, isPending: booking } = useCreateBooking()

const slots = computed(() => slotsData.value?.slots ?? [])

function toggleSlot(index: number) {
  if (!slots.value[index]?.available) return

  const idx = selectedSlots.value.indexOf(index)
  if (idx >= 0) {
    selectedSlots.value.splice(idx, 1)
  } else {
    selectedSlots.value.push(index)
    selectedSlots.value.sort((a, b) => a - b)
  }
}

function isConsecutive() {
  if (selectedSlots.value.length <= 1) return true
  for (let i = 1; i < selectedSlots.value.length; i++) {
    if (selectedSlots.value[i] !== selectedSlots.value[i - 1] + 1) return false
  }
  return true
}

const canBook = computed(() => selectedSlots.value.length > 0 && isConsecutive())

async function submit() {
  if (!canBook.value) return
  errorMsg.value = ''

  const first = slots.value[selectedSlots.value[0]]
  const last = slots.value[selectedSlots.value[selectedSlots.value.length - 1]]

  try {
    await createBooking({
      resource_type: 'slip_hoist',
      start_date: `${selectedDate.value}T${first.start}:00+02:00`,
      end_date: `${selectedDate.value}T${last.end}:00+02:00`,
    })
    selectedSlots.value = []
  } catch (err: any) {
    try {
      const parsed = JSON.parse(err.message)
      errorMsg.value = parsed.error || err.message
    } catch {
      errorMsg.value = err.message
    }
  }
}
</script>

<template>
  <div class="space-y-6">
    <div>
      <h2 class="flex items-center gap-2 text-xl font-semibold text-gray-900">
        <Wrench class="h-5 w-5 text-blue-600" />
        {{ t('booking.hoistTitle') }}
      </h2>
      <p class="mt-1 text-sm text-gray-600">{{ t('booking.hoistSubtitle') }}</p>
    </div>

    <div>
      <label class="block text-sm font-medium text-gray-700">{{ t('booking.dates') }}</label>
      <input
        v-model="selectedDate"
        type="date"
        :min="today"
        class="mt-1 rounded-md border-gray-300 text-sm"
      />
    </div>

    <div v-if="isLoading" class="animate-pulse space-y-2">
      <div v-for="i in 6" :key="i" class="h-12 rounded bg-gray-100" />
    </div>

    <div v-else class="space-y-2">
      <h3 class="text-sm font-medium text-gray-700">{{ t('booking.selectSlot') }}</h3>
      <div
        v-for="(slot, i) in slots"
        :key="i"
        class="flex items-center justify-between rounded-lg border px-4 py-3 text-sm transition"
        :class="[
          !slot.available ? 'border-gray-200 bg-gray-50 text-gray-400' : '',
          slot.available && selectedSlots.includes(i) ? 'border-blue-500 bg-blue-50 text-blue-900' : '',
          slot.available && !selectedSlots.includes(i) ? 'border-gray-200 bg-white cursor-pointer hover:border-blue-300' : '',
        ]"
        @click="toggleSlot(i)"
      >
        <span class="font-mono">{{ slot.start }} — {{ slot.end }}</span>
        <span v-if="!slot.available" class="text-xs">{{ slot.booked_by || t('booking.hoistBooked') }}</span>
        <span v-else-if="selectedSlots.includes(i)" class="text-xs font-semibold text-blue-600">{{ t('booking.selectSlot') }}</span>
      </div>
    </div>

    <div v-if="errorMsg" class="rounded-md bg-red-50 p-3 text-sm text-red-700">{{ errorMsg }}</div>

    <button
      v-if="selectedSlots.length > 0"
      type="button"
      :disabled="!canBook || booking"
      class="rounded-md bg-blue-600 px-6 py-2.5 text-sm font-semibold text-white hover:bg-blue-700 disabled:opacity-50"
      @click="submit"
    >
      {{ booking ? t('common.loading') : t('booking.confirm') }}
    </button>
  </div>
</template>
