<script setup lang="ts">
import { computed } from 'vue'

interface Series {
  key: string
  label: string
  color: string
}

interface Bucket {
  label: string
  values: Record<string, number>
}

const props = withDefaults(
  defineProps<{
    buckets: Bucket[]
    series: Series[]
    height?: number
    valueFormatter?: (v: number) => string
  }>(),
  {
    height: 200,
    valueFormatter: (v: number) => String(v),
  },
)

const maxValue = computed(() =>
  Math.max(
    1,
    ...props.buckets.flatMap((b) => props.series.map((s) => b.values[s.key] ?? 0)),
  ),
)
</script>

<template>
  <div>
    <div class="flex items-end gap-3" :style="{ height: `${height}px` }">
      <div
        v-for="(bucket, idx) in buckets"
        :key="idx"
        class="flex h-full flex-1 flex-col items-stretch justify-end gap-0.5"
      >
        <div class="flex flex-1 items-end justify-center gap-0.5">
          <div
            v-for="s in series"
            :key="s.key"
            class="group relative flex-1 rounded-t-sm transition-colors hover:brightness-110"
            :style="{
              backgroundColor: s.color,
              height: `${((bucket.values[s.key] ?? 0) / maxValue) * 100}%`,
              minHeight: (bucket.values[s.key] ?? 0) > 0 ? '2px' : '0',
            }"
          >
            <span
              class="pointer-events-none absolute -top-6 left-1/2 -translate-x-1/2 whitespace-nowrap rounded bg-gray-900 px-1.5 py-0.5 text-xs font-medium text-white opacity-0 transition-opacity group-hover:opacity-100"
            >
              {{ valueFormatter(bucket.values[s.key] ?? 0) }}
            </span>
          </div>
        </div>
      </div>
    </div>
    <div class="mt-2 flex gap-3 border-t border-gray-100 pt-2">
      <div
        v-for="(bucket, idx) in buckets"
        :key="idx"
        class="flex-1 text-center text-xs text-gray-500 tabular-nums"
      >
        {{ bucket.label }}
      </div>
    </div>
    <ul class="mt-3 flex flex-wrap gap-x-4 gap-y-1 text-xs">
      <li v-for="s in series" :key="s.key" class="flex items-center gap-1.5">
        <span class="inline-block h-2.5 w-2.5 rounded-sm" :style="{ backgroundColor: s.color }" />
        <span class="text-gray-600">{{ s.label }}</span>
      </li>
    </ul>
  </div>
</template>
