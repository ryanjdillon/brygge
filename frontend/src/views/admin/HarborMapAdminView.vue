<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  useHarborLayout,
  useUpdateHarborLayout,
  type HarborFinger,
  type HarborSlip,
  type HarborLayout,
} from '@/composables/useHarborLayout'
import HarborMap from '@/components/map/HarborMap.vue'
import { Save, Trash2, RotateCw, MousePointer2, Minus, Anchor } from 'lucide-vue-next'

const { t } = useI18n()
const { data: remote, isLoading } = useHarborLayout()
const { mutate: save, isPending: isSaving } = useUpdateHarborLayout()

type Mode = 'view' | 'finger' | 'place-slip'

const mode = ref<Mode>('view')
const pendingFingerStart = ref<{ x: number; y: number } | null>(null)
const fingers = ref<HarborFinger[]>([])
const slips = ref<HarborSlip[]>([])
const dirty = ref(false)
const selectedSlipId = ref<string | null>(null)
const placementSlipId = ref<string | null>(null)

watch(remote, (r) => {
  if (!r) return
  fingers.value = r.fingers.map((f) => ({ ...f }))
  slips.value = r.slips.map((s) => ({ ...s }))
  dirty.value = false
})

const localLayout = computed<HarborLayout | null>(() => {
  if (!remote.value) return null
  return {
    ...remote.value,
    fingers: fingers.value,
    slips: slips.value,
  }
})

const unplacedSlips = computed(() =>
  slips.value
    .filter((s) => s.map_x == null || s.map_y == null)
    .sort((a, b) => a.section.localeCompare(b.section) || a.number.localeCompare(b.number)),
)

const placedCount = computed(() => slips.value.length - unplacedSlips.value.length)

function nextFingerLabel(): string {
  const used = new Set(fingers.value.map((f) => f.label))
  for (let i = 0; i < 26; i++) {
    const c = String.fromCharCode(65 + i)
    if (!used.has(c)) return c
  }
  return ''
}

function fingerAngleDeg(f: HarborFinger): number {
  const dx = f.x2 - f.x1
  const dy = f.y2 - f.y1
  return (Math.atan2(dy, dx) * 180) / Math.PI
}

function nearestFinger(point: { x: number; y: number }): HarborFinger | null {
  let best: HarborFinger | null = null
  let bestD = Infinity
  for (const f of fingers.value) {
    const mx = (f.x1 + f.x2) / 2
    const my = (f.y1 + f.y2) / 2
    const d = Math.hypot(point.x - mx, point.y - my)
    if (d < bestD) {
      bestD = d
      best = f
    }
  }
  return best
}

function onBackgroundClick(point: { x: number; y: number }) {
  if (mode.value === 'finger') {
    if (!pendingFingerStart.value) {
      pendingFingerStart.value = point
      return
    }
    const start = pendingFingerStart.value
    pendingFingerStart.value = null
    fingers.value.push({
      id: `new-${Date.now()}`,
      label: nextFingerLabel(),
      x1: start.x,
      y1: start.y,
      x2: point.x,
      y2: point.y,
      width_m: 1.5,
      position: fingers.value.length + 1,
    })
    dirty.value = true
    return
  }

  if (mode.value === 'place-slip' && placementSlipId.value) {
    const slip = slips.value.find((s) => s.id === placementSlipId.value)
    if (!slip) return
    const finger = nearestFinger(point)
    slip.map_x = point.x
    slip.map_y = point.y
    slip.map_finger_id = finger?.id ?? null
    slip.map_rotation = finger ? fingerAngleDeg(finger) + 90 : 90
    slip.map_side = 'port'
    dirty.value = true
    placementSlipId.value = null
  }
}

function onSelectSlip(slip: HarborSlip) {
  if (mode.value !== 'view') return
  selectedSlipId.value = slip.id
}

function unplaceSelected() {
  if (!selectedSlipId.value) return
  const s = slips.value.find((x) => x.id === selectedSlipId.value)
  if (!s) return
  s.map_x = null
  s.map_y = null
  s.map_finger_id = null
  s.map_side = null
  dirty.value = true
  selectedSlipId.value = null
}

function rotateSelected(deltaDeg: number) {
  if (!selectedSlipId.value) return
  const s = slips.value.find((x) => x.id === selectedSlipId.value)
  if (!s) return
  s.map_rotation = ((s.map_rotation ?? 0) + deltaDeg) % 360
  dirty.value = true
}

function nudgeSelected(dx: number, dy: number) {
  if (!selectedSlipId.value) return
  const s = slips.value.find((x) => x.id === selectedSlipId.value)
  if (!s || s.map_x == null || s.map_y == null) return
  s.map_x += dx
  s.map_y += dy
  dirty.value = true
}

function deleteFinger(id: string) {
  const idx = fingers.value.findIndex((f) => f.id === id)
  if (idx >= 0) {
    fingers.value.splice(idx, 1)
    // Detach any slips from this finger.
    slips.value.forEach((s) => {
      if (s.map_finger_id === id) s.map_finger_id = null
    })
    dirty.value = true
  }
}

function pickSlipToPlace(id: string) {
  placementSlipId.value = id
  mode.value = 'place-slip'
}

function onSave() {
  if (!remote.value) return
  // Build payload diff — fingers: send all, marking deletions for any
  // remote finger not present locally. Slips: send all placed/unplaced.
  const remoteFingerIds = new Set(remote.value.fingers.map((f) => f.id))
  const localFingerIds = new Set(fingers.value.map((f) => f.id))
  const payload = {
    fingers: [
      ...fingers.value.map((f) => ({
        id: f.id.startsWith('new-') ? null : f.id,
        label: f.label,
        x1: f.x1,
        y1: f.y1,
        x2: f.x2,
        y2: f.y2,
        width_m: f.width_m,
        position: f.position,
      })),
      ...[...remoteFingerIds]
        .filter((id) => !localFingerIds.has(id))
        .map((id) => ({
          id,
          delete: true,
          label: '',
          x1: 0,
          y1: 0,
          x2: 0,
          y2: 0,
          width_m: null,
          position: 0,
        })),
    ],
    slips: slips.value.map((s) => ({
      id: s.id,
      map_x: s.map_x,
      map_y: s.map_y,
      map_rotation: s.map_rotation,
      map_finger_id: s.map_finger_id?.startsWith('new-') ? null : s.map_finger_id,
      map_side: s.map_side,
    })),
  }
  save(payload, {
    onSuccess: () => {
      dirty.value = false
    },
  })
}

function setMode(m: Mode) {
  mode.value = m
  pendingFingerStart.value = null
  if (m !== 'place-slip') placementSlipId.value = null
}
</script>

<template>
  <div class="flex h-[calc(100vh-4rem)] flex-col">
    <header class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 px-4 py-3">
      <div>
        <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.harborMap.title') }}</h1>
        <p class="text-sm text-gray-600">{{ t('admin.harborMap.subtitle') }}</p>
      </div>
      <div class="flex items-center gap-2">
        <span v-if="dirty" class="text-sm text-amber-600">
          {{ t('admin.harborMap.unsaved') }}
        </span>
        <button
          type="button"
          :disabled="!dirty || isSaving"
          class="inline-flex items-center gap-1.5 rounded-md bg-blue-600 px-3 py-1.5 text-sm font-medium text-white shadow-sm hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
          @click="onSave"
        >
          <Save class="h-4 w-4" />
          {{ isSaving ? t('common.saving') : t('common.save') }}
        </button>
      </div>
    </header>

    <div class="flex flex-1 overflow-hidden">
      <!-- Toolbar / sidebar -->
      <aside class="flex w-72 flex-col border-r border-gray-200 bg-gray-50">
        <div class="border-b border-gray-200 p-3">
          <p class="mb-2 text-xs font-medium uppercase tracking-wide text-gray-500">
            {{ t('admin.harborMap.tool') }}
          </p>
          <div class="grid grid-cols-3 gap-1">
            <button
              type="button"
              :class="[
                'flex flex-col items-center gap-0.5 rounded-md border p-2 text-xs',
                mode === 'view' ? 'border-blue-600 bg-blue-50 text-blue-700' : 'border-gray-200 bg-white hover:bg-gray-100',
              ]"
              @click="setMode('view')"
            >
              <MousePointer2 class="h-4 w-4" />
              {{ t('admin.harborMap.toolView') }}
            </button>
            <button
              type="button"
              :class="[
                'flex flex-col items-center gap-0.5 rounded-md border p-2 text-xs',
                mode === 'finger' ? 'border-blue-600 bg-blue-50 text-blue-700' : 'border-gray-200 bg-white hover:bg-gray-100',
              ]"
              @click="setMode('finger')"
            >
              <Minus class="h-4 w-4" />
              {{ t('admin.harborMap.toolFinger') }}
            </button>
            <button
              type="button"
              :class="[
                'flex flex-col items-center gap-0.5 rounded-md border p-2 text-xs',
                mode === 'place-slip' ? 'border-blue-600 bg-blue-50 text-blue-700' : 'border-gray-200 bg-white hover:bg-gray-100',
              ]"
              @click="setMode('place-slip')"
            >
              <Anchor class="h-4 w-4" />
              {{ t('admin.harborMap.toolSlip') }}
            </button>
          </div>
          <p v-if="mode === 'finger'" class="mt-2 text-xs text-gray-600">
            {{ pendingFingerStart ? t('admin.harborMap.fingerSecondPoint') : t('admin.harborMap.fingerFirstPoint') }}
          </p>
          <p v-else-if="mode === 'place-slip'" class="mt-2 text-xs text-gray-600">
            {{
              placementSlipId
                ? t('admin.harborMap.placeSlipClick')
                : t('admin.harborMap.placeSlipPick')
            }}
          </p>
        </div>

        <!-- Fingers list -->
        <div class="border-b border-gray-200 p-3">
          <p class="mb-2 text-xs font-medium uppercase tracking-wide text-gray-500">
            {{ t('admin.harborMap.fingers') }} ({{ fingers.length }})
          </p>
          <ul class="space-y-1">
            <li
              v-for="f in fingers"
              :key="f.id"
              class="flex items-center justify-between rounded bg-white px-2 py-1 text-sm"
            >
              <span class="font-mono">{{ f.label || '?' }}</span>
              <button
                type="button"
                class="text-gray-400 hover:text-red-600"
                :aria-label="t('common.delete')"
                @click="deleteFinger(f.id)"
              >
                <Trash2 class="h-3.5 w-3.5" />
              </button>
            </li>
            <li v-if="!fingers.length" class="text-xs italic text-gray-500">
              {{ t('admin.harborMap.noFingers') }}
            </li>
          </ul>
        </div>

        <!-- Unplaced slips queue -->
        <div class="flex-1 overflow-y-auto p-3">
          <p class="mb-2 text-xs font-medium uppercase tracking-wide text-gray-500">
            {{ t('admin.harborMap.unplacedSlips') }} ({{ unplacedSlips.length }})
          </p>
          <ul class="space-y-1">
            <li v-for="s in unplacedSlips" :key="s.id">
              <button
                type="button"
                :class="[
                  'flex w-full items-center justify-between rounded border px-2 py-1.5 text-left text-sm',
                  placementSlipId === s.id
                    ? 'border-blue-600 bg-blue-50'
                    : 'border-gray-200 bg-white hover:bg-gray-50',
                ]"
                @click="pickSlipToPlace(s.id)"
              >
                <span class="font-mono">{{ s.section ? s.section + '-' : '' }}{{ s.number }}</span>
                <span class="text-xs text-gray-500">{{ s.length_m ?? '—' }}m</span>
              </button>
            </li>
            <li v-if="!unplacedSlips.length" class="text-xs italic text-gray-500">
              {{ t('admin.harborMap.allPlaced') }}
            </li>
          </ul>
          <p class="mt-3 text-xs text-gray-500">
            {{ t('admin.harborMap.placedCount', { count: placedCount, total: slips.length }) }}
          </p>
        </div>

        <!-- Selected slip controls -->
        <div v-if="selectedSlipId" class="border-t border-gray-200 bg-white p-3">
          <p class="mb-2 text-xs font-medium uppercase tracking-wide text-gray-500">
            {{ t('admin.harborMap.selected') }}
          </p>
          <div class="flex flex-wrap gap-1">
            <button
              v-for="(d, i) in [-15, 15]"
              :key="i"
              type="button"
              class="inline-flex items-center gap-1 rounded border border-gray-200 bg-white px-2 py-1 text-xs hover:bg-gray-50"
              @click="rotateSelected(d)"
            >
              <RotateCw class="h-3 w-3" :class="{ 'scale-x-[-1]': d < 0 }" />
              {{ d > 0 ? '+15°' : '−15°' }}
            </button>
            <button
              type="button"
              class="inline-flex items-center gap-1 rounded border border-red-200 bg-white px-2 py-1 text-xs text-red-700 hover:bg-red-50"
              @click="unplaceSelected"
            >
              <Trash2 class="h-3 w-3" />
              {{ t('admin.harborMap.unplace') }}
            </button>
          </div>
          <div class="mt-2 grid grid-cols-3 gap-1">
            <span></span>
            <button class="rounded border bg-white py-0.5 text-xs hover:bg-gray-50" @click="nudgeSelected(0, -2)">↑</button>
            <span></span>
            <button class="rounded border bg-white py-0.5 text-xs hover:bg-gray-50" @click="nudgeSelected(-2, 0)">←</button>
            <span></span>
            <button class="rounded border bg-white py-0.5 text-xs hover:bg-gray-50" @click="nudgeSelected(2, 0)">→</button>
            <span></span>
            <button class="rounded border bg-white py-0.5 text-xs hover:bg-gray-50" @click="nudgeSelected(0, 2)">↓</button>
            <span></span>
          </div>
        </div>
      </aside>

      <!-- Map -->
      <main class="relative flex-1">
        <div v-if="isLoading" class="flex h-full items-center justify-center text-gray-500">
          {{ t('common.loading') }}
        </div>
        <HarborMap
          v-else-if="localLayout"
          :layout="localLayout"
          :highlight-slip-id="selectedSlipId"
          @select="onSelectSlip"
          @background-click="onBackgroundClick"
        />
      </main>
    </div>
  </div>
</template>
