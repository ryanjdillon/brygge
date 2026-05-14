<script setup lang="ts">
import { computed, ref, watch, shallowRef, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import maplibregl from 'maplibre-gl'
import {
  useHarborLayout,
  useUpdateHarborLayout,
  isFinger,
  isSlip,
  lineLengthM,
  formatSlipLabel,
  type Dock,
  type HarborLayoutResponse,
  type SlipFeature,
  type FingerFeature,
  type Point,
} from '@/composables/useHarborLayout'
import { compareSlip } from '@/lib/slipSort'
import HarborMap from '@/components/map/HarborMap.vue'
import SlipDetailPanel from '@/components/map/SlipDetailPanel.vue'
import Input from '@/components/ui/form/Input.vue'
import Textarea from '@/components/ui/form/Textarea.vue'
import RangeInput from '@/components/ui/form/RangeInput.vue'
import {
  Trash2,
  MousePointer2,
  Minus,
  Pencil,
  X,
  Lock,
  Unlock,
  MapPin,
} from 'lucide-vue-next'

const { t } = useI18n()
const { data: remote, isLoading } = useHarborLayout()
const { mutate: save, isPending: isSaving } = useUpdateHarborLayout()

type Mode = 'view' | 'finger'

const editing = ref(false)
const mode = ref<Mode>('view')
const fingers = ref<FingerFeature[]>([])
const slips = ref<SlipFeature[]>([])
const docks = ref<Dock[]>([])
const deletedFingerIds = ref<string[]>([])
const selectedFingerId = ref<string | null>(null)
const placementSlipId = ref<string | null>(null)
const slipsLocked = ref(false)
const fingersLocked = ref(true)
const editingDockSlug = ref<string | null>(null)
const selectedSlip = ref<SlipFeature | null>(null)

const LABEL_SCALE_KEY = 'brygge.harborMap.labelScale'
const labelScale = ref<number>(
  Number(localStorage.getItem(LABEL_SCALE_KEY)) || 1,
)
watch(labelScale, (v) => {
  localStorage.setItem(LABEL_SCALE_KEY, String(v))
})

const map = shallowRef<maplibregl.Map | null>(null)
const dragMarkers = shallowRef<maplibregl.Marker[]>([])
const deleteMarker = shallowRef<maplibregl.Marker | null>(null)
const previewSourceAdded = ref(false)
const dragStart = ref<[number, number] | null>(null)

watch(remote, (r) => {
  if (!r) return
  fingers.value = r.features.filter(isFinger).map((f) => ({
    ...f,
    geometry: f.geometry
      ? {
          ...f.geometry,
          coordinates: f.geometry.coordinates.map((c) => [...c] as [number, number]),
        }
      : null,
    properties: { ...f.properties },
  })) as FingerFeature[]
  slips.value = r.features.filter(isSlip).map((f) => ({
    ...f,
    geometry: f.geometry
      ? { ...f.geometry, coordinates: [...f.geometry.coordinates] as [number, number] }
      : null,
    properties: { ...f.properties },
  })) as SlipFeature[]
  docks.value = (r.docks ?? []).map((d) => ({ ...d }))
  deletedFingerIds.value = []
})

const localLayout = computed<HarborLayoutResponse | null>(() => {
  if (!remote.value) return null
  return {
    type: 'FeatureCollection',
    mode: remote.value.mode,
    features: [...fingers.value, ...slips.value] as HarborLayoutResponse['features'],
    docks: docks.value,
  }
})

const unplacedSlips = computed(() =>
  slips.value
    .filter((s) => s.geometry == null)
    .sort((a, b) => compareSlip(a.properties, b.properties)),
)

const selectedFinger = computed<FingerFeature | null>(() =>
  fingers.value.find((f) => f.id === selectedFingerId.value) ?? null,
)

const selectedFingerLengthM = computed<number>(() => {
  const f = selectedFinger.value
  if (!f?.geometry) return 0
  return lineLengthM(f.geometry.coordinates)
})

const editingDock = computed<Dock | null>(() =>
  docks.value.find((d) => d.slug === editingDockSlug.value) ?? null,
)

function newClientId(prefix: string): string {
  return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
}

let saveTimer: number | null = null

function scheduleSave() {
  if (!editing.value) return
  if (saveTimer) clearTimeout(saveTimer)
  saveTimer = window.setTimeout(flushSave, 500)
}

function flushSave() {
  if (saveTimer) {
    clearTimeout(saveTimer)
    saveTimer = null
  }
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
      docks: docks.value,
    },
    {
      onSuccess: () => {
        deletedFingerIds.value = []
      },
    },
  )
}

function setMode(m: Mode) {
  mode.value = m
  dragStart.value = null
  if (m !== 'finger') clearPreview()
  if (m === 'view') refreshDragMarkers()
  else clearDragMarkers()
}

function pickSlipToPlace(id: string) {
  if (slipsLocked.value) return
  placementSlipId.value = placementSlipId.value === id ? null : id
}

function startEditing() {
  editing.value = true
  mode.value = 'view'
  selectedFingerId.value = null
  refreshDragMarkers()
}

function stopEditing() {
  flushSave()
  editing.value = false
  mode.value = 'view'
  placementSlipId.value = null
  selectedFingerId.value = null
  editingDockSlug.value = null
  clearDragMarkers()
  clearPreview()
}

function toggleEditing() {
  if (editing.value) stopEditing()
  else startEditing()
}

function clearDragMarkers() {
  for (const m of dragMarkers.value) m.remove()
  dragMarkers.value = []
  deleteMarker.value?.remove()
  deleteMarker.value = null
  highlightSelectedFinger(null)
}

function highlightSelectedFinger(id: string | null) {
  const m = map.value
  if (!m || !m.getLayer('fingers-line')) return
  m.setPaintProperty('fingers-line', 'line-color', [
    'case',
    ['==', ['get', '_id'], id ?? ''],
    '#dc2626',
    '#0f172a',
  ])
  m.setPaintProperty('fingers-line', 'line-width', [
    'case',
    ['==', ['get', '_id'], id ?? ''],
    6,
    4,
  ])
}

function refreshDragMarkers() {
  clearDragMarkers()
  if (!editing.value || fingersLocked.value) return
  if (!map.value || !selectedFingerId.value) return
  const f = fingers.value.find((x) => x.id === selectedFingerId.value)
  if (!f?.geometry) return

  highlightSelectedFinger(selectedFingerId.value)

  for (let i = 0; i < f.geometry.coordinates.length; i++) {
    const [lng, lat] = f.geometry.coordinates[i]
    const el = document.createElement('div')
    el.style.cssText =
      'width:16px;height:16px;border-radius:50%;background:#fff;border:3px solid #dc2626;cursor:grab;box-shadow:0 1px 4px rgba(0,0,0,.4);'
    const marker = new maplibregl.Marker({ element: el, draggable: true })
      .setLngLat([lng, lat])
      .addTo(map.value)
    marker.on('drag', () => {
      const ll = marker.getLngLat()
      const updated = fingers.value.find((x) => x.id === selectedFingerId.value)
      if (!updated?.geometry) return
      updated.geometry.coordinates[i] = [ll.lng, ll.lat]
      updated.geometry = {
        type: 'LineString',
        coordinates: [...updated.geometry.coordinates],
      }
    })
    marker.on('dragend', () => scheduleSave())
    dragMarkers.value.push(marker)
  }

  const a = f.geometry.coordinates[0]
  const b = f.geometry.coordinates[1] ?? a
  const mid: [number, number] = [(a[0] + b[0]) / 2, (a[1] + b[1]) / 2]
  const x = document.createElement('button')
  x.type = 'button'
  x.textContent = '×'
  x.setAttribute('aria-label', 'Delete')
  x.style.cssText =
    'width:22px;height:22px;border-radius:50%;background:#dc2626;color:#fff;border:2px solid #fff;font-size:14px;line-height:1;font-weight:700;cursor:pointer;box-shadow:0 1px 4px rgba(0,0,0,.4);padding:0;'
  x.addEventListener('click', (ev) => {
    ev.stopPropagation()
    deleteSelectedFinger()
  })
  deleteMarker.value = new maplibregl.Marker({ element: x })
    .setLngLat(mid)
    .addTo(map.value)
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

  m.on('mousedown', (e) => {
    if (!editing.value || fingersLocked.value || mode.value !== 'finger') return
    e.preventDefault()
    m.dragPan.disable()
    dragStart.value = [e.lngLat.lng, e.lngLat.lat]
  })
  m.on('mousemove', (e) => {
    if (!editing.value || mode.value !== 'finger' || !dragStart.value) return
    setPreview(dragStart.value, [e.lngLat.lng, e.lngLat.lat])
  })
  m.on('mouseup', (e) => {
    if (!editing.value || mode.value !== 'finger' || !dragStart.value) return
    const start = dragStart.value
    const end: [number, number] = [e.lngLat.lng, e.lngLat.lat]
    dragStart.value = null
    m.dragPan.enable()
    clearPreview()

    const dx = (end[0] - start[0]) ** 2
    const dy = (end[1] - start[1]) ** 2
    if (Math.sqrt(dx + dy) < 1e-6) return

    fingers.value.push({
      type: 'Feature',
      id: newClientId('finger'),
      geometry: { type: 'LineString', coordinates: [start, end] },
      properties: {
        kind: 'finger',
        position: fingers.value.length + 1,
        notes: '',
      },
    })
    setMode('view')
    scheduleSave()
  })

  m.on('click', (e) => {
    if (!editing.value) return
    if (placementSlipId.value && !slipsLocked.value) {
      const slip = slips.value.find((s) => s.id === placementSlipId.value)
      if (!slip) return
      slip.geometry = {
        type: 'Point',
        coordinates: [e.lngLat.lng, e.lngLat.lat],
      } as Point
      placementSlipId.value = null
      scheduleSave()
      return
    }
    if (mode.value === 'view') {
      const hits = m.queryRenderedFeatures(e.point, {
        layers: ['fingers-hitbox'],
      })
      if (hits.length === 0) {
        selectedFingerId.value = null
        clearDragMarkers()
      }
    }
  })
}

function onSelectSlip(slip: SlipFeature) {
  if (editing.value) return
  selectedSlip.value = slip
}

function onSelectFinger(f: FingerFeature) {
  if (!editing.value || fingersLocked.value || mode.value !== 'view') return
  selectedFingerId.value = f.id
  refreshDragMarkers()
}

function deleteSelectedFinger() {
  if (!selectedFingerId.value) return
  const id = selectedFingerId.value
  if (id.length === 36) deletedFingerIds.value.push(id)
  fingers.value = fingers.value.filter((f) => f.id !== id)
  selectedFingerId.value = null
  clearDragMarkers()
  scheduleSave()
}

function onSlipDragend(payload: { id: string; lng: number; lat: number }) {
  if (slipsLocked.value) return
  const slip = slips.value.find((s) => s.id === payload.id)
  if (!slip) return
  slip.geometry = { type: 'Point', coordinates: [payload.lng, payload.lat] } as Point
  scheduleSave()
}

function onNotesInput(value: string) {
  const f = selectedFinger.value
  if (!f) return
  f.properties = { ...f.properties, notes: value }
  scheduleSave()
}

function flyToDock(d: Dock) {
  const m = map.value
  if (!m) return
  if (d.default_lng != null && d.default_lat != null) {
    m.flyTo({
      center: [d.default_lng, d.default_lat],
      zoom: d.default_zoom ?? m.getZoom(),
      duration: 600,
    })
  }
}

function startDockEdit(d: Dock) {
  editingDockSlug.value = d.slug
  flyToDock(d)
}

function cancelDockEdit() {
  editingDockSlug.value = null
}

function updateEditingDockView() {
  const m = map.value
  const d = editingDock.value
  if (!m || !d) return
  const c = m.getCenter()
  d.default_lng = c.lng
  d.default_lat = c.lat
  d.default_zoom = m.getZoom()
  scheduleSave()
}

function toggleSlipsLock() {
  slipsLocked.value = !slipsLocked.value
  if (slipsLocked.value) placementSlipId.value = null
}

function toggleFingersLock() {
  fingersLocked.value = !fingersLocked.value
  if (fingersLocked.value) {
    selectedFingerId.value = null
    clearDragMarkers()
    if (mode.value === 'finger') setMode('view')
  }
}

watch([fingersLocked, selectedFingerId], () => refreshDragMarkers())

const fmtMeters = (m: number) => `${m.toFixed(1)} m`

onUnmounted(() => {
  if (saveTimer) clearTimeout(saveTimer)
  clearDragMarkers()
})
</script>

<template>
  <div class="relative -m-6 h-[calc(100dvh-4rem-6rem)] lg:-m-8">
    <div v-if="isLoading" class="flex h-full items-center justify-center text-gray-500">
      {{ t('common.loading') }}
    </div>
    <HarborMap
      v-else-if="localLayout"
      :layout="localLayout"
      :highlight-slip-id="selectedSlip?.id ?? null"
      :draggable-slips="editing && !slipsLocked && !placementSlipId"
      :hidden-dock-slug="editingDockSlug"
      :label-scale="labelScale"
      @select="onSelectSlip"
      @select-finger="onSelectFinger"
      @slip-dragend="onSlipDragend"
      @map-ready="onMapReady"
    />

    <SlipDetailPanel
      v-if="!editing && selectedSlip"
      :slip="selectedSlip"
      @close="selectedSlip = null"
    />

    <!-- Centered ghost label while editing a dock view -->
    <div
      v-if="editing && editingDock"
      class="pointer-events-none absolute inset-0 z-10 flex items-center justify-center"
    >
      <div class="flex flex-col items-center">
        <div
          class="rounded-md border-2 border-dashed border-blue-600 bg-white/90 px-3 py-1 text-base font-semibold text-blue-700 shadow"
        >
          Dock {{ editingDock.name }}
        </div>
        <div class="mt-1 h-3 w-px bg-blue-600/70" />
        <div class="h-2 w-2 rounded-full bg-blue-600 ring-2 ring-white" />
      </div>
    </div>

    <!-- Edit/Close toggle (always at the same screen position) -->
    <button
      type="button"
      class="absolute left-4 top-4 z-20 inline-flex h-9 w-9 items-center justify-center rounded-md bg-white/95 text-gray-800 shadow-md ring-1 ring-gray-200 hover:bg-white"
      :aria-label="editing ? t('admin.harborMap.exitEdit') : t('common.edit')"
      @click="toggleEditing"
    >
      <X v-if="editing" class="h-4 w-4" />
      <Pencil v-else class="h-4 w-4" />
    </button>

    <!-- Title chip alongside the edit button (visible in either state) -->
    <div
      class="pointer-events-none absolute left-[calc(1rem+2.5rem+0.5rem)] top-4 z-10 inline-flex h-9 items-center rounded-md bg-white/85 px-2.5 text-sm font-semibold text-gray-800 shadow-sm ring-1 ring-gray-200"
    >
      {{ t('admin.harborMap.title') }}
      <span v-if="isSaving" class="ml-2 text-[11px] font-normal text-gray-500">
        · {{ t('common.saving') }}
      </span>
    </div>

    <!-- Centered, prominent dock shortcut buttons -->
    <div
      v-if="docks.length"
      class="pointer-events-none absolute left-1/2 top-4 z-10 flex -translate-x-1/2 flex-wrap justify-center gap-2"
    >
      <button
        v-for="d in docks"
        :key="d.slug"
        type="button"
        :class="[
          'pointer-events-auto inline-flex items-center gap-1.5 rounded-full border-2 px-4 py-2 text-sm font-semibold shadow-lg transition hover:scale-[1.03]',
          d.default_lng == null
            ? 'border-gray-300 bg-white/85 text-gray-400'
            : 'border-blue-600 bg-blue-600 text-white hover:bg-blue-700',
        ]"
        :disabled="d.default_lng == null"
        :title="
          d.default_lng == null
            ? t('admin.harborMap.dockNoView')
            : t('admin.harborMap.flyToDock', { name: d.name })
        "
        @click="flyToDock(d)"
      >
        <MapPin class="h-4 w-4" />
        Dock {{ d.name }}
      </button>
    </div>

    <!-- Floating edit menu (only when editing) -->
    <aside
      v-if="editing"
      class="absolute left-4 z-10 flex w-80 max-w-[calc(100vw-2rem)] flex-col overflow-hidden rounded-lg border border-gray-200 bg-white/95 shadow-lg backdrop-blur"
      style="top: calc(1rem + 2.5rem + 0.5rem); max-height: calc(100% - 5rem)"
    >
      <div class="flex flex-col divide-y divide-gray-200 overflow-y-auto">
        <!-- Display section -->
        <section class="px-3 py-2.5">
          <p class="mb-1 text-[10px] font-medium uppercase tracking-wide text-gray-500">
            {{ t('admin.harborMap.labelSize') }}
          </p>
          <div class="flex items-center gap-2">
            <RangeInput
              v-model="labelScale"
              :min="0.6"
              :max="2"
              :step="0.1"
              :show-value="false"
              class="flex-1"
            />
            <span class="w-10 text-right font-mono text-[11px] text-gray-700">
              {{ labelScale.toFixed(1) }}×
            </span>
          </div>
        </section>

        <!-- Docks section -->
        <section class="px-3 py-2.5">
          <p class="mb-1.5 text-[10px] font-medium uppercase tracking-wide text-gray-500">
            {{ t('admin.harborMap.docks') }}
          </p>
          <div v-if="docks.length" class="flex flex-wrap gap-1.5">
            <button
              v-for="d in docks"
              :key="d.slug"
              type="button"
              :class="[
                'rounded border px-2 py-1 text-[11px]',
                editingDockSlug === d.slug
                  ? 'border-blue-600 bg-blue-50 text-blue-700'
                  : 'border-gray-300 bg-white hover:bg-gray-50',
              ]"
              @click="
                editingDockSlug === d.slug ? cancelDockEdit() : startDockEdit(d)
              "
            >
              Dock {{ d.name }}
            </button>
          </div>
          <div v-if="editingDock" class="mt-2 rounded-md bg-blue-50/70 p-2">
            <p class="text-[11px] text-gray-700">
              {{ t('admin.harborMap.dockEditHint') }}
            </p>
            <label class="mt-1.5 block text-[10px] text-gray-500">
              {{ t('admin.harborMap.dockName') }}
            </label>
            <Input v-model="editingDock.name" @update:modelValue="scheduleSave" />
            <div class="mt-2 flex gap-1.5">
              <button
                type="button"
                class="flex-1 rounded border border-blue-300 bg-blue-600 px-2 py-1 text-[11px] font-medium text-white hover:bg-blue-700"
                @click="updateEditingDockView"
              >
                {{ t('admin.harborMap.saveDockView') }}
              </button>
              <button
                type="button"
                class="rounded border border-gray-300 bg-white px-2 py-1 text-[11px] text-gray-700 hover:bg-gray-50"
                @click="cancelDockEdit"
              >
                {{ t('common.cancel') }}
              </button>
            </div>
          </div>
        </section>

        <!-- Slips section -->
        <section class="px-3 py-2.5">
          <div class="mb-1.5 flex items-center justify-between">
            <p class="text-[10px] font-medium uppercase tracking-wide text-gray-500">
              {{ t('admin.harborMap.slips') }}
              <span class="ml-1 normal-case text-gray-400">
                ({{ unplacedSlips.length }} {{ t('admin.harborMap.unplaced') }})
              </span>
            </p>
            <button
              type="button"
              :class="[
                'inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-[11px]',
                slipsLocked
                  ? 'border-gray-300 bg-gray-50 text-gray-600'
                  : 'border-emerald-300 bg-emerald-50 text-emerald-700',
              ]"
              @click="toggleSlipsLock"
            >
              <component :is="slipsLocked ? Lock : Unlock" class="h-3 w-3" />
              {{ slipsLocked ? t('admin.harborMap.locked') : t('admin.harborMap.unlocked') }}
            </button>
          </div>
          <div v-if="!slipsLocked">
            <div v-if="unplacedSlips.length" class="flex flex-wrap gap-1.5">
              <button
                v-for="s in unplacedSlips"
                :key="s.id"
                type="button"
                :class="[
                  'rounded border px-2 py-1 font-mono text-xs',
                  placementSlipId === s.id
                    ? 'border-blue-600 bg-blue-50 text-blue-700'
                    : 'border-gray-300 bg-white hover:bg-gray-50',
                ]"
                @click="pickSlipToPlace(s.id)"
              >
                {{ formatSlipLabel(s.properties.section, s.properties.number) }}
              </button>
            </div>
            <p v-else class="text-[11px] italic text-gray-500">
              {{ t('admin.harborMap.allPlaced') }}
            </p>
            <p v-if="placementSlipId" class="mt-1.5 text-[11px] text-blue-700">
              {{ t('admin.harborMap.placeSlipClick') }}
            </p>
            <p v-else class="mt-1.5 text-[11px] text-gray-500">
              {{ t('admin.harborMap.unplaceHint') }}
            </p>
          </div>
          <p v-else class="text-[11px] italic text-gray-500">
            {{ t('admin.harborMap.slipsLockedHint') }}
          </p>
        </section>

        <!-- Dock fingers section -->
        <section class="px-3 py-2.5">
          <div class="mb-1.5 flex items-center justify-between">
            <p class="text-[10px] font-medium uppercase tracking-wide text-gray-500">
              {{ t('admin.harborMap.fingers') }}
            </p>
            <button
              type="button"
              :class="[
                'inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-[11px]',
                fingersLocked
                  ? 'border-gray-300 bg-gray-50 text-gray-600'
                  : 'border-emerald-300 bg-emerald-50 text-emerald-700',
              ]"
              @click="toggleFingersLock"
            >
              <component :is="fingersLocked ? Lock : Unlock" class="h-3 w-3" />
              {{ fingersLocked ? t('admin.harborMap.locked') : t('admin.harborMap.unlocked') }}
            </button>
          </div>
          <div v-if="!fingersLocked" class="flex flex-col gap-2">
            <div class="grid grid-cols-2 gap-1">
              <button
                type="button"
                :class="[
                  'flex items-center justify-center gap-1 rounded-md border px-2 py-1.5 text-[11px]',
                  mode === 'view'
                    ? 'border-blue-600 bg-blue-50 text-blue-700'
                    : 'border-gray-200 bg-white hover:bg-gray-100',
                ]"
                @click="setMode('view')"
              >
                <MousePointer2 class="h-3.5 w-3.5" />
                {{ t('admin.harborMap.toolView') }}
              </button>
              <button
                type="button"
                :class="[
                  'flex items-center justify-center gap-1 rounded-md border px-2 py-1.5 text-[11px]',
                  mode === 'finger'
                    ? 'border-blue-600 bg-blue-50 text-blue-700'
                    : 'border-gray-200 bg-white hover:bg-gray-100',
                ]"
                @click="setMode('finger')"
              >
                <Minus class="h-3.5 w-3.5" />
                {{ t('admin.harborMap.toolFinger') }}
              </button>
            </div>
            <p v-if="mode === 'finger'" class="text-[11px] text-gray-600">
              {{ t('admin.harborMap.fingerDragHint') }}
            </p>

            <div v-if="selectedFinger" class="rounded-md bg-amber-50/70 p-2">
              <div class="mb-1 flex items-center justify-between">
                <p class="text-[10px] font-medium uppercase tracking-wide text-gray-500">
                  {{ t('admin.harborMap.selectedFinger') }}
                </p>
                <button
                  type="button"
                  class="inline-flex items-center gap-1 rounded border border-red-200 bg-white px-1.5 py-0.5 text-[11px] text-red-700 hover:bg-red-50"
                  @click="deleteSelectedFinger"
                >
                  <Trash2 class="h-3 w-3" />
                  {{ t('common.delete') }}
                </button>
              </div>
              <div class="text-[11px] text-gray-700">
                {{ t('admin.harborMap.length') }}: {{ fmtMeters(selectedFingerLengthM) }}
              </div>
              <label class="mt-1.5 block text-[10px] font-medium uppercase tracking-wide text-gray-500">
                {{ t('admin.harborMap.notes') }}
              </label>
              <Textarea
                :model-value="selectedFinger.properties.notes ?? ''"
                :rows="2"
                class="mt-0.5"
                :placeholder="t('admin.harborMap.notesPlaceholder')"
                @update:modelValue="onNotesInput"
              />
            </div>
          </div>
          <p v-else class="text-[11px] italic text-gray-500">
            {{ t('admin.harborMap.fingersLockedHint') }}
          </p>
        </section>
      </div>
    </aside>
  </div>
</template>
