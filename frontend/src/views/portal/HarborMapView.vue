<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useHarborLayout, type SlipFeature, isSlip } from '@/composables/useHarborLayout'
import HarborMap from '@/components/map/HarborMap.vue'
import { X, Anchor, User, Mail, Sailboat, Ruler } from 'lucide-vue-next'

const { t } = useI18n()
const { data: layout, isLoading, isError } = useHarborLayout()

const selected = ref<SlipFeature | null>(null)

const stats = computed(() => {
  const slips = (layout.value?.features ?? []).filter(isSlip)
  const occupied = slips.filter(
    (f) => f.properties.occupant_id || f.properties.occupant_last_name,
  ).length
  return { total: slips.length, occupied, available: slips.length - occupied }
})

function onSelect(slip: SlipFeature) {
  selected.value = slip
}

function close() {
  selected.value = null
}
</script>

<template>
  <div class="flex h-[calc(100vh-4rem)] flex-col">
    <header class="flex flex-wrap items-center justify-between gap-3 px-4 py-3">
      <div>
        <h1 class="text-2xl font-bold text-gray-900">{{ t('portal.harborMap.title') }}</h1>
        <p class="text-sm text-gray-600">{{ t('portal.harborMap.subtitle') }}</p>
      </div>
      <div v-if="!isLoading && layout" class="flex gap-3 text-sm">
        <span class="rounded-full bg-sky-50 px-3 py-1 text-sky-700">
          {{ stats.occupied }} / {{ stats.total }} {{ t('portal.harborMap.occupied') }}
        </span>
        <span class="rounded-full bg-gray-50 px-3 py-1 text-gray-700">
          {{ stats.available }} {{ t('portal.harborMap.available') }}
        </span>
      </div>
    </header>

    <div class="relative flex-1 border-t border-gray-200">
      <div v-if="isLoading" class="flex h-full items-center justify-center text-gray-500">
        {{ t('common.loading') }}
      </div>
      <div v-else-if="isError" class="flex h-full items-center justify-center text-red-600">
        {{ t('common.error') }}
      </div>
      <HarborMap
        v-else-if="layout"
        :layout="layout"
        :highlight-slip-id="selected?.id ?? null"
        @select="onSelect"
      />

      <aside
        v-if="selected"
        class="absolute right-0 top-0 z-10 flex h-full w-full max-w-md flex-col overflow-y-auto border-l border-gray-200 bg-white shadow-xl"
        role="dialog"
        :aria-label="t('portal.harborMap.details')"
      >
        <div class="flex items-start justify-between border-b border-gray-200 p-4">
          <div>
            <p class="text-xs font-medium uppercase tracking-wide text-gray-500">
              {{ t('portal.harborMap.slip') }}
            </p>
            <h2 class="text-xl font-bold text-gray-900">
              {{ selected.properties.section ? selected.properties.section + '-' : '' }}{{ selected.properties.number }}
            </h2>
          </div>
          <button
            type="button"
            class="rounded-md p-1 text-gray-500 hover:bg-gray-100"
            :aria-label="t('common.close')"
            @click="close"
          >
            <X class="h-5 w-5" />
          </button>
        </div>

        <dl class="space-y-3 p-4 text-sm">
          <div class="flex items-center gap-2">
            <Anchor class="h-4 w-4 text-gray-500" />
            <dt class="font-medium text-gray-700">{{ t('portal.harborMap.assignmentType') }}:</dt>
            <dd class="text-gray-900">
              <template v-if="selected.properties.assignment_type === 'permanent'">
                {{ t('portal.harborMap.permanent') }}
              </template>
              <template v-else-if="selected.properties.assignment_type === 'seasonal'">
                {{ t('portal.harborMap.seasonal') }}
              </template>
              <template v-else>—</template>
            </dd>
          </div>

          <div v-if="selected.properties.length_m || selected.properties.width_m" class="flex items-center gap-2">
            <Ruler class="h-4 w-4 text-gray-500" />
            <dt class="font-medium text-gray-700">{{ t('portal.harborMap.dimensions') }}:</dt>
            <dd class="text-gray-900">
              {{ selected.properties.length_m ?? '—' }}m × {{ selected.properties.width_m ?? '—' }}m
            </dd>
          </div>

          <div v-if="selected.properties.occupant_name || selected.properties.occupant_last_name" class="flex items-center gap-2">
            <User class="h-4 w-4 text-gray-500" />
            <dt class="font-medium text-gray-700">{{ t('portal.harborMap.holder') }}:</dt>
            <dd class="text-gray-900">
              {{ selected.properties.occupant_name ?? selected.properties.occupant_last_name }}
            </dd>
          </div>

          <div v-if="selected.properties.occupant_email" class="flex items-center gap-2">
            <Mail class="h-4 w-4 text-gray-500" />
            <dt class="font-medium text-gray-700">{{ t('portal.harborMap.email') }}:</dt>
            <dd>
              <a :href="`mailto:${selected.properties.occupant_email}`" class="text-sky-600 hover:underline">
                {{ selected.properties.occupant_email }}
              </a>
            </dd>
          </div>

          <div v-if="selected.properties.boat_name" class="flex items-center gap-2">
            <Sailboat class="h-4 w-4 text-gray-500" />
            <dt class="font-medium text-gray-700">{{ t('portal.harborMap.boat') }}:</dt>
            <dd class="text-gray-900">
              {{ selected.properties.boat_name }}
              <span v-if="selected.properties.boat_length_m" class="text-gray-500">
                ({{ selected.properties.boat_length_m }}m)
              </span>
            </dd>
          </div>

          <div v-if="!selected.properties.occupant_last_name && !selected.properties.occupant_id" class="rounded-md bg-emerald-50 p-3 text-emerald-800">
            {{ t('portal.harborMap.availableNotice') }}
          </div>
        </dl>
      </aside>
    </div>
  </div>
</template>
