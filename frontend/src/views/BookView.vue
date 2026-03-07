<script setup lang="ts">
import { ref, reactive, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import { Anchor, Truck, Home, Wrench } from 'lucide-vue-next'
import BookingCalendar from '@/components/booking/BookingCalendar.vue'
import BoatDimensionsForm from '@/components/booking/BoatDimensionsForm.vue'
import BookingConfirmation from '@/components/booking/BookingConfirmation.vue'
import PhoneInput from '@/components/booking/PhoneInput.vue'
import { useCreateBooking, type Booking } from '@/composables/useBookings'
import { usePricing } from '@/composables/usePricing'
import { useAuthStore } from '@/stores/auth'

const { t } = useI18n()
const route = useRoute()
const auth = useAuthStore()

const resourceTypes = [
  { key: 'guest_slip', icon: Anchor, label: 'booking.guestSlip' },
  { key: 'bobil_spot', icon: Truck, label: 'booking.bobilSpot' },
  { key: 'club_room', icon: Home, label: 'booking.clubRoom' },
  { key: 'slip_hoist', icon: Wrench, label: 'booking.slipHoist' },
]

const selectedType = ref((route.query.type as string) || '')
const step = ref(1)
const startDate = ref<string | undefined>()
const endDate = ref<string | undefined>()
const boatDimensions = ref({ length: null as number | null, beam: null as number | null, draft: null as number | null })
const guestName = ref('')
const guestEmail = ref('')
const guestPhone = ref('')
const notes = ref('')
const confirmedBooking = ref<Booking | null>(null)
const errorMessage = ref('')
const fieldErrors = reactive<Record<string, string>>({})

const needsDimensions = computed(() => ['guest_slip', 'shared_slip', 'seasonal_rental'].includes(selectedType.value))
const isSlipType = computed(() => needsDimensions.value)

const { mutateAsync: createBooking, isPending: isSubmitting } = useCreateBooking()

const { items: priceItems, unitLabel } = usePricing()

const pricingCategoryMap: Record<string, string> = {
  guest_slip: 'guest',
  bobil_spot: 'bobil',
  club_room: 'room_hire',
  slip_hoist: 'service',
}

const priceEstimate = computed(() => {
  const cat = pricingCategoryMap[selectedType.value]
  if (!cat) return null
  const item = priceItems.value.find((p) => p.category === cat)
  if (!item) return null

  let nights = 0
  if (startDate.value && endDate.value) {
    const ms = new Date(endDate.value).getTime() - new Date(startDate.value).getTime()
    nights = Math.max(1, Math.round(ms / 86_400_000))
  }

  const perUnit = item.amount
  const unit = item.unit
  const total = (unit === 'day' || unit === 'night') && nights > 0 ? perUnit * nights : perUnit

  return { perUnit, unit, total, nights }
})

const emailRegex = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

function validateField(field: 'name' | 'email' | 'phone') {
  if (auth.isAuthenticated) return

  if (field === 'name') {
    fieldErrors.name = !guestName.value.trim() ? t('booking.validation.nameRequired') : ''
  } else if (field === 'email') {
    if (!guestEmail.value.trim()) {
      fieldErrors.email = t('booking.validation.emailRequired')
    } else if (!emailRegex.test(guestEmail.value.trim())) {
      fieldErrors.email = t('booking.validation.emailInvalid')
    } else {
      fieldErrors.email = ''
    }
  } else if (field === 'phone') {
    const digits = guestPhone.value.replace(/[\s+\-]/g, '')
    if (digits && digits.length < 6) {
      fieldErrors.phone = t('booking.validation.phoneInvalid')
    } else {
      fieldErrors.phone = ''
    }
  }
}

function validateFields(): boolean {
  validateField('name')
  validateField('email')
  validateField('phone')
  return !fieldErrors.name && !fieldErrors.email && !fieldErrors.phone
}

function selectType(key: string) {
  selectedType.value = key
  step.value = isSlipType.value ? 2 : 3
}

function onDimensionsNext() {
  if (!boatDimensions.value.length || !boatDimensions.value.beam || !boatDimensions.value.draft) return
  step.value = 3
}

function onDatesNext() {
  if (!startDate.value || !endDate.value) return
  step.value = 4
}

async function submitBooking() {
  errorMessage.value = ''

  if (!validateFields()) return

  try {
    const booking = await createBooking({
      resource_type: selectedType.value,
      start_date: startDate.value!,
      end_date: endDate.value!,
      boat_length_m: boatDimensions.value.length ?? undefined,
      boat_beam_m: boatDimensions.value.beam ?? undefined,
      boat_draft_m: boatDimensions.value.draft ?? undefined,
      guest_name: auth.isAuthenticated ? undefined : guestName.value || undefined,
      guest_email: auth.isAuthenticated ? undefined : guestEmail.value || undefined,
      guest_phone: auth.isAuthenticated ? undefined : guestPhone.value || undefined,
      notes: notes.value || undefined,
    })
    confirmedBooking.value = booking
    step.value = 5
  } catch (err: any) {
    try {
      const parsed = JSON.parse(err.message)
      errorMessage.value = parsed.error || err.message
    } catch {
      errorMessage.value = err.message || t('booking.error')
    }
  }
}

function startOver() {
  selectedType.value = ''
  step.value = 1
  startDate.value = undefined
  endDate.value = undefined
  boatDimensions.value = { length: null, beam: null, draft: null }
  guestName.value = ''
  guestEmail.value = ''
  guestPhone.value = ''
  notes.value = ''
  confirmedBooking.value = null
  errorMessage.value = ''
  fieldErrors.name = ''
  fieldErrors.email = ''
  fieldErrors.phone = ''
}
</script>

<template>
  <div class="mx-auto max-w-2xl px-4 py-12 sm:px-6 lg:px-8">
    <h1 class="text-3xl font-bold text-gray-900">{{ t('booking.title') }}</h1>
    <p class="mt-1 text-gray-600">{{ t('booking.subtitle') }}</p>

    <!-- Step indicator -->
    <div v-if="step < 5" class="mt-6 flex items-center gap-2 text-sm text-gray-500">
      <span v-for="s in 4" :key="s" class="flex items-center gap-1">
        <span
          class="flex h-6 w-6 items-center justify-center rounded-full text-xs font-semibold"
          :class="s <= step ? 'bg-blue-600 text-white' : 'bg-gray-200 text-gray-500'"
        >
          {{ s }}
        </span>
        <span v-if="s < 4" class="h-px w-6 bg-gray-300" />
      </span>
    </div>

    <!-- Step 1: Select resource type -->
    <div v-if="step === 1" class="mt-8 grid gap-4 sm:grid-cols-2">
      <button
        v-for="rt in resourceTypes"
        :key="rt.key"
        type="button"
        class="flex items-center gap-4 rounded-lg border border-gray-200 bg-white p-5 text-left transition hover:border-blue-400 hover:shadow-md"
        @click="selectType(rt.key)"
      >
        <component :is="rt.icon" class="h-8 w-8 text-blue-600" />
        <span class="font-semibold text-gray-900">{{ t(rt.label) }}</span>
      </button>
    </div>

    <!-- Step 2: Boat dimensions (slip types only) -->
    <div v-else-if="step === 2" class="mt-8">
      <h2 class="text-lg font-semibold text-gray-900">{{ t('booking.boatDimensions') }}</h2>
      <div class="mt-4">
        <BoatDimensionsForm v-model="boatDimensions" />
      </div>
      <div class="mt-6 flex gap-3">
        <button type="button" class="rounded-md border px-4 py-2 text-sm" @click="step = 1">
          {{ t('common.back') }}
        </button>
        <button
          type="button"
          class="rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white hover:bg-blue-700 disabled:opacity-50"
          :disabled="!boatDimensions.length || !boatDimensions.beam || !boatDimensions.draft"
          @click="onDimensionsNext"
        >
          {{ t('common.next') }}
        </button>
      </div>
    </div>

    <!-- Step 3: Select dates -->
    <div v-else-if="step === 3" class="mt-8">
      <h2 class="text-lg font-semibold text-gray-900">{{ t('booking.selectDates') }}</h2>
      <div class="mt-4 rounded-lg border border-gray-200 bg-white p-4">
        <BookingCalendar
          :resource-type="selectedType"
          :dimensions="needsDimensions ? boatDimensions : undefined"
          v-model:start-date="startDate"
          v-model:end-date="endDate"
        />
      </div>
      <div v-if="startDate && endDate" class="mt-3 text-sm text-gray-600">
        {{ startDate }} — {{ endDate }}
      </div>
      <div class="mt-6 flex gap-3">
        <button type="button" class="rounded-md border px-4 py-2 text-sm" @click="step = isSlipType ? 2 : 1">
          {{ t('common.back') }}
        </button>
        <button
          type="button"
          class="rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white hover:bg-blue-700 disabled:opacity-50"
          :disabled="!startDate || !endDate"
          @click="onDatesNext"
        >
          {{ t('common.next') }}
        </button>
      </div>
    </div>

    <!-- Step 4: Guest details & confirm -->
    <div v-else-if="step === 4" class="mt-8 space-y-6">
      <h2 class="text-lg font-semibold text-gray-900">{{ t('booking.details') }}</h2>

      <div v-if="!auth.isAuthenticated" class="space-y-4">
        <div>
          <label class="block text-sm font-medium text-gray-700">{{ t('booking.guestNameLabel') }}</label>
          <input
            v-model="guestName"
            type="text"
            class="mt-1 block w-full rounded-md text-sm focus:border-blue-500 focus:ring-blue-500"
            :class="fieldErrors.name ? 'border-red-400' : 'border-gray-300'"
            @blur="validateField('name')"
          />
          <p v-if="fieldErrors.name" class="mt-1 text-sm text-red-600">{{ fieldErrors.name }}</p>
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700">{{ t('booking.guestEmail') }}</label>
          <input
            v-model="guestEmail"
            type="email"
            class="mt-1 block w-full rounded-md text-sm focus:border-blue-500 focus:ring-blue-500"
            :class="fieldErrors.email ? 'border-red-400' : 'border-gray-300'"
            @blur="validateField('email')"
          />
          <p v-if="fieldErrors.email" class="mt-1 text-sm text-red-600">{{ fieldErrors.email }}</p>
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700">{{ t('booking.guestPhone') }}</label>
          <PhoneInput v-model="guestPhone" :has-error="!!fieldErrors.phone" @blur="validateField('phone')" />
          <p v-if="fieldErrors.phone" class="mt-1 text-sm text-red-600">{{ fieldErrors.phone }}</p>
        </div>
      </div>

      <div>
        <label class="block text-sm font-medium text-gray-700">{{ t('booking.notes') }}</label>
        <textarea
          v-model="notes"
          rows="2"
          class="mt-1 block w-full rounded-md border-gray-300 text-sm focus:border-blue-500 focus:ring-blue-500"
        />
      </div>

      <div class="rounded-lg border border-gray-200 bg-gray-50 p-4 text-sm">
        <h3 class="font-semibold text-gray-900">{{ t('booking.summary') }}</h3>
        <dl class="mt-2 space-y-1">
          <div class="flex justify-between">
            <dt class="text-gray-500">{{ t('booking.type') }}</dt>
            <dd>{{ t(`booking.${selectedType === 'guest_slip' ? 'guestSlip' : selectedType === 'bobil_spot' ? 'bobilSpot' : selectedType === 'club_room' ? 'clubRoom' : 'slipHoist'}`) }}</dd>
          </div>
          <div class="flex justify-between">
            <dt class="text-gray-500">{{ t('booking.dates') }}</dt>
            <dd>{{ startDate }} — {{ endDate }}</dd>
          </div>
          <div v-if="boatDimensions.length" class="flex justify-between">
            <dt class="text-gray-500">{{ t('booking.boatDimensions') }}</dt>
            <dd>{{ boatDimensions.length }}m × {{ boatDimensions.beam }}m × {{ boatDimensions.draft }}m</dd>
          </div>
          <template v-if="priceEstimate">
            <div class="flex justify-between">
              <dt class="text-gray-500">{{ t('booking.unitPrice') }}</dt>
              <dd>{{ priceEstimate.perUnit }} NOK{{ unitLabel(priceEstimate.unit) }}</dd>
            </div>
            <div class="flex justify-between border-t border-gray-200 pt-1 font-semibold text-gray-900">
              <dt>{{ t('booking.estimatedTotal') }}</dt>
              <dd>{{ priceEstimate.total }} NOK</dd>
            </div>
          </template>
        </dl>
      </div>

      <div v-if="errorMessage" class="rounded-md bg-red-50 p-3 text-sm text-red-700">
        {{ errorMessage }}
      </div>

      <div class="flex gap-3">
        <button type="button" class="rounded-md border px-4 py-2 text-sm" @click="step = 3">
          {{ t('common.back') }}
        </button>
        <button
          type="button"
          class="rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white hover:bg-blue-700 disabled:opacity-50"
          :disabled="isSubmitting || (!auth.isAuthenticated && (!guestName || !guestEmail))"
          @click="submitBooking"
        >
          {{ isSubmitting ? t('common.loading') : t('booking.confirm') }}
        </button>
      </div>
    </div>

    <!-- Step 5: Confirmation -->
    <div v-else-if="step === 5 && confirmedBooking" class="mt-8">
      <BookingConfirmation :booking="confirmedBooking" />
      <div class="mt-6 text-center">
        <button
          type="button"
          class="text-sm font-medium text-blue-600 hover:text-blue-700"
          @click="startOver"
        >
          {{ t('booking.bookAnother') }}
        </button>
      </div>
    </div>
  </div>
</template>
