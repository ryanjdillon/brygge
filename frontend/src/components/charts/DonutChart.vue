<script setup lang="ts">
import { computed } from 'vue'

interface Slice {
  label: string
  value: number
  color: string
}

const props = withDefaults(
  defineProps<{
    slices: Slice[]
    size?: number
    thickness?: number
    centerLabel?: string
    centerValue?: string
  }>(),
  { size: 180, thickness: 28 },
)

// Polar → cartesian, returning a string ready to drop into a path d.
function arcPath(cx: number, cy: number, r: number, startAngle: number, endAngle: number, innerR: number): string {
  const toXY = (angle: number, radius: number) => {
    const rad = (angle - 90) * (Math.PI / 180)
    return { x: cx + radius * Math.cos(rad), y: cy + radius * Math.sin(rad) }
  }
  const startOuter = toXY(startAngle, r)
  const endOuter = toXY(endAngle, r)
  const startInner = toXY(endAngle, innerR)
  const endInner = toXY(startAngle, innerR)
  const largeArc = endAngle - startAngle > 180 ? 1 : 0
  return [
    `M ${startOuter.x} ${startOuter.y}`,
    `A ${r} ${r} 0 ${largeArc} 1 ${endOuter.x} ${endOuter.y}`,
    `L ${startInner.x} ${startInner.y}`,
    `A ${innerR} ${innerR} 0 ${largeArc} 0 ${endInner.x} ${endInner.y}`,
    'Z',
  ].join(' ')
}

const total = computed(() => props.slices.reduce((acc, s) => acc + Math.max(0, s.value), 0))

interface RenderedSlice extends Slice {
  d: string
  pct: number
}

const renderedSlices = computed<RenderedSlice[]>(() => {
  const cx = props.size / 2
  const cy = props.size / 2
  const r = props.size / 2 - 2
  const innerR = r - props.thickness
  if (total.value <= 0) return []

  let angle = 0
  return props.slices
    .filter((s) => s.value > 0)
    .map((s) => {
      const pct = s.value / total.value
      const sweep = pct * 360
      // Tiny gap between slices so adjacent segments don't blur.
      const startAngle = angle + 0.4
      const endAngle = angle + sweep - 0.4
      angle += sweep
      return {
        ...s,
        d: arcPath(cx, cy, r, startAngle, endAngle, innerR),
        pct,
      }
    })
})
</script>

<template>
  <div class="flex items-center gap-5">
    <div class="relative shrink-0" :style="{ width: `${size}px`, height: `${size}px` }">
      <svg :width="size" :height="size" :viewBox="`0 0 ${size} ${size}`" role="img">
        <!-- Empty-state ring -->
        <circle
          v-if="total <= 0"
          :cx="size / 2"
          :cy="size / 2"
          :r="size / 2 - 2 - thickness / 2"
          fill="none"
          stroke="rgb(229 231 235)"
          :stroke-width="thickness"
        />
        <path
          v-for="(s, i) in renderedSlices"
          :key="i"
          :d="s.d"
          :fill="s.color"
        >
          <title>{{ s.label }}: {{ Math.round(s.pct * 100) }}%</title>
        </path>
      </svg>
      <!-- Center label is constrained to the donut's inner ring so a
           wide number like "303 000 kr" can't bleed past the slices.
           padding = thickness keeps the text well inside the hole. -->
      <div
        v-if="centerLabel || centerValue"
        class="absolute inset-0 flex flex-col items-center justify-center text-center leading-tight"
        :style="{ padding: `${thickness + 4}px` }"
      >
        <p v-if="centerValue" class="text-sm font-semibold text-gray-900 tabular-nums tracking-tight">{{ centerValue }}</p>
        <p v-if="centerLabel" class="mt-0.5 text-xs text-gray-500">{{ centerLabel }}</p>
      </div>
    </div>
    <ul class="space-y-1.5 text-sm">
      <li v-for="(s, i) in slices" :key="i" class="flex items-center gap-2">
        <span class="inline-block h-2.5 w-2.5 rounded-sm" :style="{ backgroundColor: s.color }" />
        <span class="text-gray-700">{{ s.label }}</span>
        <span class="ml-auto text-gray-500 tabular-nums">{{ s.value > 0 ? Math.round((s.value / Math.max(1, total)) * 100) + '%' : '—' }}</span>
      </li>
    </ul>
  </div>
</template>
