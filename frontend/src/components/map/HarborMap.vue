<script setup lang="ts">
import { computed, ref, onMounted, onBeforeUnmount } from 'vue'
import type { HarborLayout, HarborSlip, HarborFinger } from '@/composables/useHarborLayout'

interface Props {
  layout: HarborLayout
  highlightSlipId?: string | null
  showLabels?: boolean
  interactive?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  highlightSlipId: null,
  showLabels: true,
  interactive: true,
})

const emit = defineEmits<{
  (e: 'select', slip: HarborSlip): void
  (e: 'select-finger', finger: HarborFinger): void
  (e: 'background-click', point: { x: number; y: number }): void
}>()

const svgEl = ref<SVGSVGElement | null>(null)

// Pan/zoom state — small custom transform; no extra dep.
const scale = ref(1)
const tx = ref(0)
const ty = ref(0)

const dragging = ref(false)
const dragStart = ref<{ x: number; y: number; tx: number; ty: number } | null>(null)

const viewBox = computed(() => props.layout.view_box.join(' '))

const transform = computed(
  () => `translate(${tx.value} ${ty.value}) scale(${scale.value})`,
)

// Per-finger boat-length-along-finger placement in SVG units.
// SVG viewBox is 757x463; we don't have a real-world scale, so length_m
// maps to a small rendered length proportional to the viewBox.
function boatLengthSvg(slip: HarborSlip): number {
  const fallback = 18
  if (!slip.length_m) return fallback
  // 1m ≈ 1.6 svg units (tunable). Tweak when fingers are placed.
  return Math.max(8, slip.length_m * 1.6)
}

function boatWidthSvg(slip: HarborSlip): number {
  const fallback = 8
  const w = slip.boat_beam_m ?? slip.width_m
  if (!w) return fallback
  return Math.max(5, w * 1.6)
}

function colorFor(slip: HarborSlip): string {
  if (slip.status !== 'occupied' && !slip.occupant_id && !slip.occupant_last_name) {
    return 'transparent'
  }
  if (slip.assignment_type === 'seasonal') return '#f59e0b' // amber
  return '#0ea5e9' // sky / permanent
}

function strokeFor(slip: HarborSlip): string {
  if (props.highlightSlipId === slip.id) return '#dc2626'
  if (slip.status === 'occupied' || slip.occupant_id || slip.occupant_last_name) {
    return '#0c4a6e'
  }
  return '#94a3b8'
}

function isOccupied(slip: HarborSlip): boolean {
  return Boolean(slip.occupant_last_name || slip.occupant_id)
}

function onSlipClick(slip: HarborSlip, ev: MouseEvent) {
  ev.stopPropagation()
  if (!props.interactive) return
  emit('select', slip)
}

function onWheel(ev: WheelEvent) {
  if (!props.interactive) return
  ev.preventDefault()
  const delta = -ev.deltaY * 0.001
  const next = Math.min(8, Math.max(0.4, scale.value * (1 + delta)))
  // Zoom toward cursor.
  const rect = svgEl.value?.getBoundingClientRect()
  if (!rect) {
    scale.value = next
    return
  }
  const cx = ev.clientX - rect.left
  const cy = ev.clientY - rect.top
  const ratio = next / scale.value
  tx.value = cx - (cx - tx.value) * ratio
  ty.value = cy - (cy - ty.value) * ratio
  scale.value = next
}

function onPointerDown(ev: PointerEvent) {
  if (!props.interactive) return
  if (ev.button !== 0) return
  dragging.value = true
  dragStart.value = { x: ev.clientX, y: ev.clientY, tx: tx.value, ty: ty.value }
  ;(ev.target as Element).setPointerCapture?.(ev.pointerId)
}

function onPointerMove(ev: PointerEvent) {
  if (!dragging.value || !dragStart.value) return
  tx.value = dragStart.value.tx + (ev.clientX - dragStart.value.x)
  ty.value = dragStart.value.ty + (ev.clientY - dragStart.value.y)
}

function onPointerUp() {
  dragging.value = false
  dragStart.value = null
}

function onBackgroundClick(ev: MouseEvent) {
  if (!svgEl.value) return
  // Already handled by pointer drag distance? Skip if any drag occurred.
  const pt = svgEl.value.createSVGPoint()
  pt.x = ev.clientX
  pt.y = ev.clientY
  const ctm = svgEl.value.getScreenCTM()
  if (!ctm) return
  const local = pt.matrixTransform(ctm.inverse())
  emit('background-click', { x: local.x, y: local.y })
}

const onResetZoom = () => {
  scale.value = 1
  tx.value = 0
  ty.value = 0
}

defineExpose({ resetZoom: onResetZoom })

const keyHandler = (ev: KeyboardEvent) => {
  if (ev.key === '0' && (ev.ctrlKey || ev.metaKey)) {
    ev.preventDefault()
    onResetZoom()
  }
}
onMounted(() => window.addEventListener('keydown', keyHandler))
onBeforeUnmount(() => window.removeEventListener('keydown', keyHandler))

// Harbor outline path imported once from havenkart.svg.
const harborPath =
  'm 689,391.11836 -3.5,-0.61836 0.0666,-12 c 0.5756,-103.77181 2.26383,-199.02037 2.23254,-199.1589 0.2562,-4.41891 0.2009,-7.5268 0.2009,-11.27371 0,-3.43107 0.32005,-4.06739 2.04578,-4.06739 1.96699,0 2.02269,-0.39478 1.4461,-10.25 -0.73601,-12.57998 -1.31222,-17.90368 -2.01374,-18.60519 -0.61532,-0.61532 -0.70288,-0.62908 -9.71609,-1.52688 -8.27677,-0.82444 -11.73105,-2.79092 -18.56497,-10.5688 -8.27826,-9.42172 -8.33291,-9.41919 -12.12368,0.56037 -1.77897,4.6833 -3.78843,8.72765 -4.46547,8.98746 -0.67704,0.2598 -6.3783,-1.49054 -12.66946,-3.88964 -6.29116,-2.39911 -13.47907,-5.11492 -15.97314,-6.03514 -2.85967,-1.05511 -5.16546,-2.74096 -6.24238,-4.56404 -1.60716,-2.7207 -1.60792,-3.16013 -0.0129,-7.46375 0.93214,-2.51506 1.46744,-4.80021 1.18954,-5.07811 C 610.06175,104.72841 504.35726,98.272294 483,97.754544 L 472.5,97.5 l -0.68365,8.98199 c -0.43975,5.77759 -0.31891,9.20741 0.33871,9.61384 1.11268,0.68768 0.79469,5.48372 -1.58495,23.90417 -0.81709,6.325 -1.71741,13.75 -2.0007,16.5 -1.31183,12.73434 -21.43669,178.45996 -22.26822,183.37598 l -0.57104,3.37597 -5.33937,-0.55107 c -2.93665,-0.30309 -5.71436,-0.78283 -6.1727,-1.0661 -0.45833,-0.28326 -0.0426,-7.40447 0.92382,-15.8249 0.96643,-8.42044 2.02843,-18.23488 2.36001,-21.80988 0.702,-7.56879 2.95388,-27.78668 4.45523,-40 1.44512,-11.75581 10.01401,-87.59321 11.07305,-98 1.48748,-14.61672 4.91157,-44.34952 5.499,-47.75 0.39585,-2.29147 1.10638,-3.25 2.40913,-3.25 2.44483,0 2.74202,-1.05429 2.97851,-10.56653 l 0.20055,-8.066535 -23.80869,-1.220023 C 427.21391,94.4759 413.8,93.727203 410.5,93.483143 c -3.3,-0.244061 -15,-0.920606 -26,-1.503433 -11,-0.582827 -31.7,-1.718156 -46,-2.522954 -14.3,-0.804797 -30.725,-1.694306 -36.5,-1.976688 -5.775,-0.282381 -13.94353,-0.77386 -18.15228,-1.092175 l -7.65228,-0.578755 -0.67608,8.845431 c -0.37184,4.864987 -0.89614,9.632931 -1.16511,10.595431 -0.34877,1.24809 0.087,1.75 1.51935,1.75 3.03244,0 3.31889,1.27845 2.18125,9.73498 -0.57454,4.27076 -1.25432,10.24002 -1.51062,13.26502 -0.25631,3.025 -1.13404,10.45 -1.95053,16.5 -0.81649,6.05 -3.1098,24.5 -5.09626,41 -4.44318,36.90611 -6.38289,51.50563 -6.96913,52.45419 -0.48329,0.78199 -12.98999,-1.05672 -13.97117,-2.05402 -0.56655,-0.57586 0.97177,-14.7201 7.38478,-67.90017 1.62496,-13.475 3.63515,-30.35 4.4671,-37.5 0.83195,-7.15 1.82253,-15.63263 2.20128,-18.8503 0.41337,-3.51173 0.99211,-7.61201 2.42578,-7.5 2.58926,0.20229 1.7088,-0.90888 2.10053,-11.1497 L 267.5,85.5 254,84.852376 c -13.98723,-0.670996 -33.99577,-1.488186 -59,-2.40968 -7.975,-0.293906 -17.2,-0.712778 -20.5,-0.930824 -3.3,-0.218047 -16.6159,-0.711003 -29.59089,-1.095458 l -23.59088,-0.699009 -0.65312,8.391297 C 119.69062,100.62896 119.78012,102 121.57196,102 c 1.32976,0 1.462,0.88591 0.85828,5.75 -5.92126,47.70685 -9.15423,69.73818 -10.22076,69.65 -0.66521,-0.055 -3.12198,-0.17439 -5.45948,-0.26531 -2.3375,-0.0909 -4.77838,-0.50139 -5.42417,-0.91215 -1.471752,-0.93612 6.99483,-76.18465 8.68978,-77.232187 0.60042,-0.371082 1.98995,1.037507 2.01793,-7.082524 0.0236,-6.860067 2.62441,-11.976161 0.29027,-12.116091 C 104.28821,79.310011 92.837852,78.821506 84,78.580424 69.975,78.238526 53.758357,77.717756 47.963015,77.423158 41.735331,77.106583 36.313605,77.310469 34.705959,77.921695 33.209919,78.490488 26.926562,80.311227 20.742943,81.967781 14.559325,83.624335 7.3625,85.637742 4.75,86.442018 L 0,87.904338 V 43.952169 0 h 81.428571 c 66.788679,0 81.669859,0.24128728 82.770639,1.3420641 2.47279,2.4727903 5.25506,3.4060381 12.30079,4.1260041 3.85,0.3934114 14.03182,2.2131147 22.62627,4.0437851 18.73712,3.9911217 19.65118,4.1217957 41.87373,5.9862717 24.64282,2.067536 31.54839,2.380235 66,2.988613 16.775,0.296229 37.7,0.870397 46.5,1.27593 21.17161,0.975657 41.03738,0.93307 51.5,-0.110401 15.9737,-1.593111 36.96305,-1.573403 46,0.04319 6.09349,1.090046 14.58706,1.479276 30,1.374793 11.825,-0.08016 32.975,0.541382 47,1.381207 14.025,0.839825 32.89254,1.531812 41.92787,1.537748 L 586.35574,24 l 24.2685,8.000667 c 13.34768,4.400366 30.03014,9.198766 37.07214,10.66311 L 660.5,45.32622 l 13,9.67123 c 7.15,5.319177 14.35,10.522883 16,11.563792 1.65,1.040909 3.225,2.151249 3.5,2.467423 0.95114,1.093549 10.53987,7.294496 20.47978,13.244091 10.74211,6.429762 11.17811,6.837239 14.90419,13.929158 2.39677,4.561816 2.43301,4.935266 1.21637,12.533686 -0.96416,6.02157 -2.21647,9.44192 -5.41809,14.79808 C 718.70504,132.69679 715.14012,135 706.43463,135 h -6.59343 l 0.70662,8.75 c 0.38865,4.8125 0.83994,9.425 1.00289,10.25 0.16295,0.825 0.3307,3.22963 0.37278,5.34362 0.0596,2.99236 0.52857,3.96184 2.11767,4.37739 1.62745,0.42559 1.83677,1.26473 1.8646,4.15638 0.13331,13.85095 -1.03381,24.56441 -0.92199,40.87261 -0.0182,26.66866 -0.75427,87.22761 -1.47371,121.25 -0.26169,12.375 -0.4835,31.3875 -0.49292,42.25 L 703,392 l -5.25,-0.13164 c -2.8875,-0.0724 -6.825,-0.4099 -8.75,-0.75 z M 699.62119,160.25 c -0.30503,-2.0625 -0.82984,-7.8 -1.16625,-12.75 -0.76811,-11.30201 -1.1155,-12.5 -3.62477,-12.5 -1.98299,0 -2.01663,0.35177 -1.31481,13.75 0.39614,7.5625 0.9188,14.0875 1.16146,14.5 0.24267,0.4125 1.57921,0.75 2.97009,0.75 2.3542,0 2.49058,-0.25904 1.97428,-3.75 z M 469.375,112.875 c 1.02753,-5.13767 0.71481,-15.701462 -0.4868,-16.444098 -2.01134,-1.243075 -2.85735,1.942957 -2.87289,10.819098 -0.0118,6.765 0.29662,8.75 1.35969,8.75 0.75625,0 1.65625,-1.40625 2,-3.125 z M 272.39177,102.75 C 274.08401,88.766154 274.04101,86 272.1314,86 c -1.5674,0 -1.93312,1.126139 -2.48466,7.650878 -0.96153,11.374862 -0.9682,12.853932 0.66819,12.853932 0.94527,0 1.73892,-0.96246 2.07684,-3.75481 z M 117.3493,99.25 c 0.26623,-0.9625 0.78034,-5.9125 1.14246,-11 0.56389,-7.922458 0.43884,-9.25 -0.87137,-9.25 -1.6492,0 -2.11979,1.875101 -3.44529,13.727803 -0.76957,6.881652 -0.51983,7.900947 2.00751,8.193247 0.37544,0.0434 0.90045,-0.70855 1.16669,-1.67105 z'
</script>

<template>
  <div class="relative h-full w-full overflow-hidden bg-sky-50 select-none">
    <svg
      ref="svgEl"
      :viewBox="viewBox"
      class="h-full w-full"
      :class="{ 'cursor-grab': interactive && !dragging, 'cursor-grabbing': dragging }"
      preserveAspectRatio="xMidYMid meet"
      role="img"
      aria-label="Harbor map"
      @wheel="onWheel"
      @pointerdown="onPointerDown"
      @pointermove="onPointerMove"
      @pointerup="onPointerUp"
      @pointercancel="onPointerUp"
      @click="onBackgroundClick"
    >
      <g :transform="transform">
        <!-- Harbor outline (land) -->
        <path :d="harborPath" fill="#e7e5e4" stroke="#a8a29e" stroke-width="0.5" />

        <!-- Dock fingers -->
        <g>
          <line
            v-for="finger in props.layout.fingers"
            :key="finger.id"
            :x1="finger.x1"
            :y1="finger.y1"
            :x2="finger.x2"
            :y2="finger.y2"
            stroke="#1f2937"
            stroke-width="2.5"
            stroke-linecap="round"
            class="cursor-pointer"
            @click.stop="emit('select-finger', finger)"
          />
        </g>

        <!-- Slips -->
        <g>
          <template v-for="slip in props.layout.slips" :key="slip.id">
            <g
              v-if="slip.map_x != null && slip.map_y != null"
              :transform="`translate(${slip.map_x} ${slip.map_y}) rotate(${slip.map_rotation})`"
              class="cursor-pointer"
              @click="onSlipClick(slip, $event)"
            >
              <!-- Boat silhouette (rounded rect with pointed bow) -->
              <path
                v-if="isOccupied(slip)"
                :d="`M ${-boatLengthSvg(slip) / 2} ${-boatWidthSvg(slip) / 2}
                     L ${boatLengthSvg(slip) / 2 - boatWidthSvg(slip) / 2} ${-boatWidthSvg(slip) / 2}
                     L ${boatLengthSvg(slip) / 2} 0
                     L ${boatLengthSvg(slip) / 2 - boatWidthSvg(slip) / 2} ${boatWidthSvg(slip) / 2}
                     L ${-boatLengthSvg(slip) / 2} ${boatWidthSvg(slip) / 2} Z`"
                :fill="colorFor(slip)"
                :stroke="strokeFor(slip)"
                stroke-width="0.6"
                fill-opacity="0.85"
              />
              <!-- Empty slip indicator -->
              <circle
                v-else
                :r="Math.max(boatWidthSvg(slip) / 2, 4)"
                fill="none"
                :stroke="strokeFor(slip)"
                stroke-width="0.8"
                stroke-dasharray="2 1.5"
              />
              <!-- Highlight ring -->
              <circle
                v-if="props.highlightSlipId === slip.id"
                :r="Math.max(boatLengthSvg(slip) / 2 + 3, 8)"
                fill="none"
                stroke="#dc2626"
                stroke-width="1.2"
                class="animate-pulse"
              />
              <text
                v-if="showLabels && (isOccupied(slip) ? slip.occupant_last_name : slip.number)"
                :y="boatWidthSvg(slip) / 2 + 4"
                text-anchor="middle"
                font-size="3.5"
                fill="#0f172a"
                font-weight="500"
                style="paint-order: stroke; stroke: white; stroke-width: 1px;"
              >
                {{ isOccupied(slip) ? slip.occupant_last_name : slip.number }}
              </text>
            </g>
          </template>
        </g>
      </g>
    </svg>

    <!-- Controls -->
    <div
      v-if="interactive"
      class="absolute right-3 top-3 flex flex-col gap-1 rounded-md border border-gray-200 bg-white/90 p-1 text-sm shadow-sm backdrop-blur"
    >
      <button
        type="button"
        class="rounded px-2 py-1 hover:bg-gray-100"
        aria-label="Zoom in"
        @click.stop="scale = Math.min(8, scale * 1.25)"
      >
        +
      </button>
      <button
        type="button"
        class="rounded px-2 py-1 hover:bg-gray-100"
        aria-label="Zoom out"
        @click.stop="scale = Math.max(0.4, scale / 1.25)"
      >
        −
      </button>
      <button
        type="button"
        class="rounded px-2 py-1 hover:bg-gray-100"
        aria-label="Reset"
        @click.stop="onResetZoom"
      >
        ⟳
      </button>
    </div>

    <!-- Legend -->
    <div
      class="absolute bottom-3 left-3 flex items-center gap-3 rounded-md border border-gray-200 bg-white/90 px-3 py-1.5 text-xs shadow-sm backdrop-blur"
    >
      <span class="flex items-center gap-1">
        <span class="inline-block h-3 w-4 rounded-sm bg-sky-500"></span>
        Permanent
      </span>
      <span class="flex items-center gap-1">
        <span class="inline-block h-3 w-4 rounded-sm bg-amber-500"></span>
        Seasonal
      </span>
      <span class="flex items-center gap-1">
        <span class="inline-block h-3 w-3 rounded-full border border-dashed border-gray-500"></span>
        Available
      </span>
    </div>
  </div>
</template>
