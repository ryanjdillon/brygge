<script setup lang="ts">
import { computed, ref, watch, shallowRef, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import maplibregl from 'maplibre-gl'
import {
  useHarborLayout,
  useUpdateHarborLayout,
  isFinger,
  isSlip,
  type HarborLayoutResponse,
  type SlipFeature,
  type FingerFeature,
  type Point,
} from '@/composables/useHarborLayout'
import HarborMap from '@/components/map/HarborMap.vue'
import { Save, Trash2, MousePointer2, Minus, Anchor } from 'lucide-vue-next'

const { t } = useI18n()
const { data: remote, isLoading } = useHarborLayout()
const { mutate: save, isPending: isSaving } = useUpdateHarborLayout()

type Mode = 'view' | 'finger' | 'place-slip'

const mode = ref<Mode>('view')
const fingers = ref<FingerFeature[]>([])
const slips = ref<SlipFeature[]>([])
const deletedFingerIds = ref<string[]>([])
const dirty = ref(false)
const selectedFingerId = ref<string | null>(null)
const placementSlipId = ref<string | null>(null)

const map = shallowRef<maplibregl.Map | null>(null)
const dragMarkers = shallowRef<maplibregl.Marker[]>([])
const previewSourceAdded = ref(false)
const dragStart = ref<[number, number] | null>(null)

watch(remote, (r) => {
  if (!r) return
  fingers.value = r.features.filter(isFinger).map((f) => ({
    ...f,
    geometry: f.geometry ? { ...f.geometry, coordinates: f.geometry.coordinates.map((c) => [...c] as [number, number]) } : null,
    properties: { ...f.properties },
  })) as FingerFeature[]
  slips.value = r.features.filter(isSlip).map((f) => ({
    ...f,
    geometry: f.geometry ? { ...f.geometry, coordinates: [...f.geometry.coordinates] as [number, number] } : null,
    properties: { ...f.properties },
  })) as SlipFeature[]
  deletedFingerIds.value = []
  dirty.value = false
})

const localLayout = computed<HarborLayoutResponse | null>(() => {
  if (!remote.value) return null
  return {
    type: 'FeatureCollection',
    mode: remote.value.mode,
    features: [...fingers.value, ...slips.value] as HarborLayoutResponse['features'],
  }
})

const unplacedSlips = computed(() =>
  slips.value
    .filter((s) => s.geometry == null)
    .sort((a, b) => {
      const aKey = `${a.properties.section}-${a.properties.number}`
      const bKey = `${b.properties.section}-${b.properties.number}`
      return aKey.localeCompare(bKey)
    }),
)

const placedCount = computed(
  () => slips.value.length - unplacedSlips.value.length,
)

function newClientId(prefix: string): string {
  return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
}

function setMode(m: Mode) {
  mode.value = m
  dragStart.value = null
  if (m !== 'place-slip') placementSlipId.value = null
  if (m !== 'finger') clearPreview()
  if (m === 'view') refreshDragMarkers()
  else clearDragMarkers()
}

function pickSlipToPlace(id: string) {
  placementSlipId.value = id
  setMode('place-slip')
}

function clearDragMarkers() {
  for (const m of dragMarkers.value) m.remove()
  dragMarkers.value = []
}

function refreshDragMarkers() {
  clearDragMarkers()
  if (!map.value || !selectedFingerId.value) return
  const f = fingers.value.find((x) => x.id === selectedFingerId.value)
  if (!f?.geometry) return
  for (let i = 0; i < f.geometry.coordinates.length; i++) {
    const [lng, lat] = f.geometry.coordinates[i]
    const el = document.createElement('div')
    el.style.cssText =
      'width:14px;height:14px;border-radius:50%;background:#fff;border:2px solid #dc2626;cursor:grab;'
    const marker = new maplibregl.Marker({ element: el, draggable: true })
      .setLngLat([lng, lat])
      .addTo(map.value)
    marker.on('drag', () => {
      const ll = marker.getLngLat()
      const updated = fingers.value.find((x) => x.id === selectedFingerId.value)
      if (!updated?.geometry) return
      updated.geometry.coordinates[i] = [ll.lng, ll.lat]
      // Trigger reactivity by replacing the array reference.
      updated.geometry = {
        type: 'LineString',
        coordinates: [...updated.geometry.coordinates],
      }
      dirty.value = true
    })
    dragMarkers.value.push(marker)
  }
}

function ensurePreviewSource() {
  const m = map.value
  if (!m || previewSourceAdded.value) return
  m.addSource('finger-preview', {
    type: 'geojson',
    data: { type: 'FeatureCollection', features: [] },
  })
  m.addLayer({
    id: 'finger-preview-line',
    type: 'line',
    source: 'finger-preview',
    paint: {
      'line-color': '#dc2626',
      'line-width': 3,
      'line-dasharray': [2, 2],
    },
  })
  previewSourceAdded.value = true
}

function setPreview(start: [number, number] | null, end: [number, number] | null) {
  const m = map.value
  if (!m) return
  ensurePreviewSource()
  const src = m.getSource('finger-preview') as maplibregl.GeoJSONSource | undefined
  if (!src) return
  if (!start || !end) {
    src.setData({ type: 'FeatureCollection', features: [] })
    return
  }
  src.setData({
    type: 'FeatureCollection',
    features: [
      {
        type: 'Feature',
        geometry: { type: 'LineString', coordinates: [start, end] },
        properties: {},
      },
    ],
  } as never)
}

function clearPreview() {
  setPreview(null, null)
}

function onMapReady(m: maplibregl.Map) {
  map.value = m

  // Drag-to-create finger: pointerdown + pointermove + pointerup.
  m.on('mousedown', (e) => {
    if (mode.value !== 'finger') return
    e.preventDefault()
    m.dragPan.disable()
    dragStart.value = [e.lngLat.lng, e.lngLat.lat]
  })
  m.on('mousemove', (e) => {
    if (mode.value !== 'finger' || !dragStart.value) return
    setPreview(dragStart.value, [e.lngLat.lng, e.lngLat.lat])
  })
  m.on('mouseup', (e) => {
    if (mode.value !== 'finger' || !dragStart.value) return
    const start = dragStart.value
    const end: [number, number] = [e.lngLat.lng, e.lngLat.lat]
    dragStart.value = null
    m.dragPan.enable()
    clearPreview()

    const dx = (end[0] - start[0]) ** 2
    const dy = (end[1] - start[1]) ** 2
    if (Math.sqrt(dx + dy) < 1e-6) return // ignore tiny clicks (no drag)

    fingers.value.push({
      type: 'Feature',
      id: newClientId('finger'),
      geometry: { type: 'LineString', coordinates: [start, end] },
      properties: {
        kind: 'finger',
        position: fingers.value.length + 1,
      },
    })
    dirty.value = true
  })

  // Place a queued slip on click.
  m.on('click', (e) => {
    if (mode.value !== 'place-slip' || !placementSlipId.value) return
    const slip = slips.value.find((s) => s.id === placementSlipId.value)
    if (!slip) return
    slip.geometry = {
      type: 'Point',
      coordinates: [e.lngLat.lng, e.lngLat.lat],
    } as Point
    dirty.value = true
    placementSlipId.value = null
    setMode('view')
  })
}

function onSelectFinger(f: FingerFeature) {
  if (mode.value !== 'view') return
  selectedFingerId.value = f.id
  refreshDragMarkers()
}

function deleteSelectedFinger() {
  if (!selectedFingerId.value) return
  const id = selectedFingerId.value
  // Only push to deletedFingerIds if it's a server-side id (UUID-like).
  if (id.length === 36) {
    deletedFingerIds.value.push(id)
  }
  fingers.value = fingers.value.filter((f) => f.id !== id)
  selectedFingerId.value = null
  clearDragMarkers()
  dirty.value = true
}

function unplaceSelectedSlip(slipId: string) {
  const slip = slips.value.find((s) => s.id === slipId)
  if (!slip) return
  slip.geometry = null
  dirty.value = true
}

function onSave() {
  if (!remote.value) return
  save(
    {
      type: 'FeatureCollection',
      features: [
        ...fingers.value.map((f) => ({
          type: 'Feature' as const,
          id: f.id.length === 36 ? f.id : '',
          geometry: f.geometry,
          properties: f.properties,
        })),
        ...slips.value.map((s) => ({
          type: 'Feature' as const,
          id: s.id,
          geometry: s.geometry,
          properties: s.properties,
        })),
      ],
      deleted_finger_ids: deletedFingerIds.value,
    },
    {
      onSuccess: () => {
        dirty.value = false
      },
    },
  )
}

onUnmounted(() => {
  clearDragMarkers()
})
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
            {{ t('admin.harborMap.fingerDragHint') }}
          </p>
          <p v-else-if="mode === 'place-slip'" class="mt-2 text-xs text-gray-600">
            {{
              placementSlipId
                ? t('admin.harborMap.placeSlipClick')
                : t('admin.harborMap.placeSlipPick')
            }}
          </p>
          <p v-else-if="selectedFingerId" class="mt-2 text-xs text-gray-600">
            {{ t('admin.harborMap.fingerSelectedHint') }}
          </p>
        </div>

        <div v-if="selectedFingerId" class="border-b border-gray-200 bg-white p-3">
          <p class="mb-2 text-xs font-medium uppercase tracking-wide text-gray-500">
            {{ t('admin.harborMap.selectedFinger') }}
          </p>
          <button
            type="button"
            class="inline-flex items-center gap-1.5 rounded border border-red-200 bg-white px-2 py-1 text-xs text-red-700 hover:bg-red-50"
            @click="deleteSelectedFinger"
          >
            <Trash2 class="h-3 w-3" />
            {{ t('common.delete') }}
          </button>
        </div>

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
                <span class="font-mono">{{ s.properties.section ? s.properties.section + '-' : '' }}{{ s.properties.number }}</span>
                <span class="text-xs text-gray-500">{{ s.properties.length_m ?? '—' }}m</span>
              </button>
            </li>
            <li v-if="!unplacedSlips.length" class="text-xs italic text-gray-500">
              {{ t('admin.harborMap.allPlaced') }}
            </li>
          </ul>
          <p class="mt-3 text-xs text-gray-500">
            {{ t('admin.harborMap.placedCount', { count: placedCount, total: slips.length }) }}
          </p>

          <!-- Placed slips list with unplace action -->
          <p v-if="slips.length - unplacedSlips.length > 0" class="mb-2 mt-4 text-xs font-medium uppercase tracking-wide text-gray-500">
            {{ t('admin.harborMap.placedSlips') }}
          </p>
          <ul class="space-y-1">
            <li v-for="s in slips.filter((x) => x.geometry != null)" :key="s.id" class="flex items-center justify-between rounded bg-white px-2 py-1 text-sm">
              <span class="font-mono">{{ s.properties.section ? s.properties.section + '-' : '' }}{{ s.properties.number }}</span>
              <button
                type="button"
                class="text-gray-400 hover:text-red-600"
                :aria-label="t('admin.harborMap.unplace')"
                @click="unplaceSelectedSlip(s.id)"
              >
                <Trash2 class="h-3.5 w-3.5" />
              </button>
            </li>
          </ul>
        </div>
      </aside>

      <main class="relative flex-1">
        <div v-if="isLoading" class="flex h-full items-center justify-center text-gray-500">
          {{ t('common.loading') }}
        </div>
        <HarborMap
          v-else-if="localLayout"
          :layout="localLayout"
          :highlight-slip-id="null"
          @select-finger="onSelectFinger"
          @map-ready="onMapReady"
        />
      </main>
    </div>
  </div>
</template>
