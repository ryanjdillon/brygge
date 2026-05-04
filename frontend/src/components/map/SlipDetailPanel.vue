<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { X, User, Phone, Mail, Sailboat, Ruler } from 'lucide-vue-next'
import { formatSlipLabel, type SlipFeature } from '@/composables/useHarborLayout'

const props = defineProps<{ slip: SlipFeature }>()
defineEmits<{ (e: 'close'): void }>()

const { t } = useI18n()

const label = computed(() =>
  formatSlipLabel(props.slip.properties.section, props.slip.properties.number),
)

const typeChipClass = computed(() => {
  switch (props.slip.properties.assignment_type) {
    case 'permanent':
      return 'bg-sky-100 text-sky-800 ring-sky-200'
    case 'seasonal':
      return 'bg-amber-100 text-amber-800 ring-amber-200'
    default:
      return 'bg-emerald-100 text-emerald-800 ring-emerald-200'
  }
})

const typeChipLabel = computed(() => {
  switch (props.slip.properties.assignment_type) {
    case 'permanent':
      return t('portal.harborMap.permanent')
    case 'seasonal':
      return t('portal.harborMap.seasonal')
    default:
      return t('portal.harborMap.available')
  }
})

const ownerName = computed(() => {
  const p = props.slip.properties
  return p.occupant_name || p.occupant_last_name || ''
})

const boatHeadline = computed(() => {
  const p = props.slip.properties
  const parts = [p.boat_manufacturer, p.boat_model].filter(Boolean)
  const head = parts.join(' ').trim()
  return head || p.boat_name || ''
})

const hasBoat = computed(() => {
  const p = props.slip.properties
  return Boolean(
    p.boat_id ||
      p.boat_name ||
      p.boat_manufacturer ||
      p.boat_model ||
      p.boat_length_m ||
      p.boat_beam_m,
  )
})
</script>

<template>
  <div
    class="absolute inset-0 z-20 flex items-center justify-center p-4"
    role="presentation"
    v-backdrop-close="() => $emit('close')"
  >
    <aside
      class="flex max-h-full w-full max-w-sm flex-col overflow-y-auto rounded-lg border border-gray-200 bg-white shadow-2xl"
      role="dialog"
      :aria-label="t('portal.harborMap.details')"
      @click.stop
    >
      <div class="flex items-start gap-3 border-b border-gray-200 p-4">
        <div class="min-w-0 shrink-0">
          <p class="text-xs font-medium uppercase tracking-wide text-gray-500">
            {{ t('portal.harborMap.slip') }}
          </p>
          <h2 class="text-2xl font-bold text-gray-900">{{ label }}</h2>
        </div>
        <span
          :class="[
            'ml-auto mt-1 inline-flex shrink-0 items-center rounded-full px-2.5 py-1 text-xs font-semibold ring-1',
            typeChipClass,
          ]"
        >
          {{ typeChipLabel }}
        </span>
        <button
          type="button"
          class="rounded-md p-1 text-gray-500 hover:bg-gray-100"
          :aria-label="t('common.close')"
          @click="$emit('close')"
        >
          <X class="h-5 w-5" />
        </button>
      </div>

      <dl class="space-y-3 p-4 text-sm">
        <div v-if="ownerName" class="flex items-center gap-2">
          <User class="h-4 w-4 text-gray-500" />
          <dt class="font-medium text-gray-700">{{ t('portal.harborMap.holder') }}:</dt>
          <dd class="text-gray-900">{{ ownerName }}</dd>
        </div>

        <div v-if="slip.properties.occupant_phone" class="flex items-center gap-2">
          <Phone class="h-4 w-4 text-gray-500" />
          <dt class="font-medium text-gray-700">{{ t('portal.harborMap.phone') }}:</dt>
          <dd>
            <a
              :href="`tel:${slip.properties.occupant_phone}`"
              class="text-sky-600 hover:underline"
            >
              {{ slip.properties.occupant_phone }}
            </a>
          </dd>
        </div>

        <div v-if="slip.properties.occupant_email" class="flex items-center gap-2">
          <Mail class="h-4 w-4 text-gray-500" />
          <dt class="font-medium text-gray-700">{{ t('portal.harborMap.email') }}:</dt>
          <dd>
            <a
              :href="`mailto:${slip.properties.occupant_email}`"
              class="text-sky-600 hover:underline"
            >
              {{ slip.properties.occupant_email }}
            </a>
          </dd>
        </div>

        <div v-if="hasBoat" class="flex items-start gap-2">
          <Sailboat class="mt-0.5 h-4 w-4 shrink-0 text-gray-500" />
          <div class="min-w-0">
            <dt class="font-medium text-gray-700">{{ t('portal.harborMap.boat') }}</dt>
            <dd v-if="boatHeadline || slip.properties.boat_name" class="text-gray-900">
              {{ boatHeadline || slip.properties.boat_name }}
            </dd>
            <dd v-else class="text-gray-500 italic">
              {{ t('portal.harborMap.boatUnnamed') }}
            </dd>
            <dd
              v-if="slip.properties.boat_beam_m || slip.properties.boat_length_m"
              class="text-xs text-gray-500"
            >
              <span v-if="slip.properties.boat_beam_m">
                {{ t('portal.harborMap.beam') }}
                {{ slip.properties.boat_beam_m }}m
              </span>
              <span
                v-if="slip.properties.boat_beam_m && slip.properties.boat_length_m"
              >
                ·
              </span>
              <span v-if="slip.properties.boat_length_m">
                {{ t('portal.harborMap.length') }}
                {{ slip.properties.boat_length_m }}m
              </span>
            </dd>
          </div>
        </div>

        <div
          v-if="slip.properties.length_m || slip.properties.width_m"
          class="flex items-center gap-2"
        >
          <Ruler class="h-4 w-4 text-gray-500" />
          <dt class="font-medium text-gray-700">{{ t('portal.harborMap.dimensions') }}:</dt>
          <dd class="text-gray-900">
            {{ slip.properties.length_m ?? '—' }}m × {{ slip.properties.width_m ?? '—' }}m
          </dd>
        </div>

        <div
          v-if="!slip.properties.occupant_last_name && !slip.properties.occupant_id"
          class="rounded-md bg-emerald-50 p-3 text-emerald-800"
        >
          {{ t('portal.harborMap.availableNotice') }}
        </div>
      </dl>
    </aside>
  </div>
</template>
