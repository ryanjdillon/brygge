<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useHarborLayout, type SlipFeature, isSlip } from '@/composables/useHarborLayout'
import HarborMap from '@/components/map/HarborMap.vue'
import SlipDetailPanel from '@/components/map/SlipDetailPanel.vue'

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
  <div class="relative -m-6 h-[calc(100dvh-4rem-6rem)] lg:-m-8">
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

    <div
      v-if="!isLoading && layout"
      class="pointer-events-none absolute left-0 top-0 z-10 flex"
    >
      <div
        class="pointer-events-auto m-4 flex flex-col gap-1 rounded-lg border border-gray-200 bg-white/95 px-3 py-2 shadow backdrop-blur"
      >
        <h1 class="text-base font-semibold text-gray-900">
          {{ t('portal.harborMap.title') }}
        </h1>
        <p class="text-xs text-gray-500">{{ t('portal.harborMap.subtitle') }}</p>
        <div class="mt-1 flex flex-wrap gap-2 text-[11px]">
          <span class="rounded-full bg-sky-50 px-2 py-0.5 text-sky-700">
            {{ stats.occupied }} / {{ stats.total }} {{ t('portal.harborMap.occupied') }}
          </span>
          <span class="rounded-full bg-gray-50 px-2 py-0.5 text-gray-700">
            {{ stats.available }} {{ t('portal.harborMap.available') }}
          </span>
        </div>
      </div>
    </div>

    <SlipDetailPanel v-if="selected" :slip="selected" @close="close" />
  </div>
</template>
