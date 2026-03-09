<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ChevronLeft, ChevronRight } from 'lucide-vue-next'
import { useAggregateAvailability, type BoatDimensions } from '@/composables/useBookings'

const props = defineProps<{
  resourceType: string
  dimensions?: BoatDimensions
}>()

const startDate = defineModel<string>('startDate')
const endDate = defineModel<string>('endDate')

const { t } = useI18n()

const today = new Date()
const todayStr = today.toISOString().slice(0, 10)
const currentMonth = ref(today.getMonth())
const currentYear = ref(today.getFullYear())

const monthStart = computed(() => {
  const d = new Date(currentYear.value, currentMonth.value, 1)
  return d.toISOString().slice(0, 10)
})

const monthEnd = computed(() => {
  const d = new Date(currentYear.value, currentMonth.value + 1, 0)
  return d.toISOString().slice(0, 10)
})

const monthLabel = computed(() => {
  const d = new Date(currentYear.value, currentMonth.value, 1)
  return d.toLocaleDateString('nb-NO', { month: 'long', year: 'numeric' })
})

const typeRef = computed(() => props.resourceType)
const dimsRef = computed(() => props.dimensions)
const { data: availData, isLoading } = useAggregateAvailability(typeRef, monthStart, monthEnd, dimsRef)

const availMap = computed(() => {
  const map = new Map<string, { available: number; total: number }>()
  for (const d of availData.value?.dates ?? []) {
    map.set(d.date, { available: d.available_units, total: d.total_units })
  }
  return map
})

interface CalendarDay {
  date: string
  day: number
  inMonth: boolean
  isPast: boolean
  available?: number
  total?: number
}

const calendarDays = computed<CalendarDay[]>(() => {
  const first = new Date(currentYear.value, currentMonth.value, 1)
  const startDay = (first.getDay() + 6) % 7
  const daysInMonth = new Date(currentYear.value, currentMonth.value + 1, 0).getDate()

  const days: CalendarDay[] = []

  for (let i = 0; i < startDay; i++) {
    days.push({ date: '', day: 0, inMonth: false, isPast: false })
  }

  for (let d = 1; d <= daysInMonth; d++) {
    const dateStr = `${currentYear.value}-${String(currentMonth.value + 1).padStart(2, '0')}-${String(d).padStart(2, '0')}`
    const avail = availMap.value.get(dateStr)
    days.push({
      date: dateStr,
      day: d,
      inMonth: true,
      isPast: dateStr < todayStr,
      available: avail?.available,
      total: avail?.total,
    })
  }

  return days
})

const allUnavailable = computed(() => {
  const inMonthDays = calendarDays.value.filter((d) => d.inMonth && !d.isPast)
  if (inMonthDays.length === 0) return true
  return inMonthDays.every((d) => d.available === 0)
})

const isPrevDisabled = computed(() => {
  return currentYear.value === today.getFullYear() && currentMonth.value === today.getMonth()
})

function prevMonth() {
  if (isPrevDisabled.value) return
  if (currentMonth.value === 0) {
    currentMonth.value = 11
    currentYear.value--
  } else {
    currentMonth.value--
  }
}

function nextMonth() {
  if (currentMonth.value === 11) {
    currentMonth.value = 0
    currentYear.value++
  } else {
    currentMonth.value++
  }
}

function isDayDisabled(day: CalendarDay) {
  if (!day.inMonth || day.isPast) return true
  if (day.available != null && day.available === 0) return true
  return false
}

function selectDate(date: string) {
  if (!date) return
  if (!startDate.value || (startDate.value && endDate.value)) {
    startDate.value = date
    endDate.value = undefined
  } else if (date > startDate.value) {
    endDate.value = date
  } else {
    startDate.value = date
    endDate.value = undefined
  }
}

function isInRange(date: string) {
  if (!startDate.value || !endDate.value || !date) return false
  return date >= startDate.value && date <= endDate.value
}

function dayClass(day: CalendarDay) {
  if (!day.inMonth) return 'invisible'

  const isStart = day.date === startDate.value
  const isEnd = day.date === endDate.value
  const inRange = isInRange(day.date)
  const disabled = isDayDisabled(day)

  if (isStart || isEnd) return 'bg-blue-600 text-white font-semibold'
  if (inRange) return 'bg-blue-100 text-blue-900'
  if (disabled) return 'bg-gray-100 text-gray-300 cursor-not-allowed'

  if (day.total != null && day.available != null) {
    const pct = day.available / day.total
    if (pct > 0.5) return 'bg-green-50 text-green-800 hover:ring-2 hover:ring-blue-400'
    if (pct > 0) return 'bg-yellow-50 text-yellow-800 hover:ring-2 hover:ring-blue-400'
  }

  return 'hover:ring-2 hover:ring-blue-400'
}

const weekdays = computed(() => [
  t('calendar.weekdayMon'),
  t('calendar.weekdayTue'),
  t('calendar.weekdayWed'),
  t('calendar.weekdayThu'),
  t('calendar.weekdayFri'),
  t('calendar.weekdaySat'),
  t('calendar.weekdaySun'),
])
</script>

<template>
  <div>
    <div class="mb-3 flex items-center justify-between">
      <button
        type="button"
        class="rounded p-1 hover:bg-gray-100 disabled:opacity-30 disabled:cursor-not-allowed"
        :disabled="isPrevDisabled"
        @click="prevMonth"
      >
        <ChevronLeft class="h-5 w-5" />
      </button>
      <span class="text-sm font-semibold capitalize text-gray-900">{{ monthLabel }}</span>
      <button type="button" class="rounded p-1 hover:bg-gray-100" @click="nextMonth">
        <ChevronRight class="h-5 w-5" />
      </button>
    </div>

    <div v-if="isLoading" class="flex h-48 items-center justify-center text-sm text-gray-400">
      {{ t('common.loading') }}...
    </div>

    <template v-else>
      <div class="grid grid-cols-7 gap-1 text-center text-xs text-gray-500">
        <div v-for="wd in weekdays" :key="wd" class="py-1 font-medium">{{ wd }}</div>
      </div>

      <div class="mt-1 grid grid-cols-7 gap-1">
        <button
          v-for="(day, i) in calendarDays"
          :key="i"
          type="button"
          :disabled="isDayDisabled(day)"
          class="relative h-10 rounded text-sm transition"
          :class="dayClass(day)"
          @click="selectDate(day.date)"
        >
          {{ day.day || '' }}
        </button>
      </div>

      <div v-if="allUnavailable" class="mt-4 rounded-md bg-amber-50 border border-amber-200 p-3 text-sm text-amber-700">
        {{ t('booking.noAvailability') }}
      </div>
    </template>
  </div>
</template>
