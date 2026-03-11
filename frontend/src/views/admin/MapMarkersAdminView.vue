<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQueryClient } from '@tanstack/vue-query'
import { Plus, Pencil, Trash2 } from 'lucide-vue-next'
import { useMapMarkers, type MapMarker } from '@/composables/useMap'
import { useApiClient, unwrap } from '@/lib/apiClient'

const { t } = useI18n()
const client = useApiClient()
const queryClient = useQueryClient()
const { data: markers, isLoading } = useMapMarkers()

const showModal = ref(false)
const editing = ref<MapMarker | null>(null)

const form = ref({
  marker_type: 'waypoint',
  label: '',
  lat: 0,
  lng: 0,
  sort_order: 0,
})

const markerTypes = ['waypoint', 'buoy', 'hazard', 'anchorage', 'harbour'] as const

function openCreate() {
  editing.value = null
  form.value = { marker_type: 'waypoint', label: '', lat: 0, lng: 0, sort_order: 0 }
  showModal.value = true
}

function openEdit(m: MapMarker) {
  editing.value = m
  form.value = {
    marker_type: m.marker_type,
    label: m.label,
    lat: m.lat,
    lng: m.lng,
    sort_order: m.sort_order,
  }
  showModal.value = true
}

const saving = ref(false)

async function save() {
  saving.value = true
  try {
    if (editing.value) {
      await unwrap(await client.PUT('/api/v1/admin/map/markers/{markerID}', {
        params: { path: { markerID: editing.value.id } },
        body: form.value as any,
      }))
    } else {
      await unwrap(await client.POST('/api/v1/admin/map/markers', {
        body: form.value as any,
      }))
    }
    queryClient.invalidateQueries({ queryKey: ['map', 'markers'] })
    showModal.value = false
  } finally {
    saving.value = false
  }
}

async function deleteMarker(m: MapMarker) {
  if (!confirm(t('mapAdmin.deleteConfirm'))) return
  await unwrap(await client.DELETE('/api/v1/admin/map/markers/{markerID}', { params: { path: { markerID: m.id } } }))
  queryClient.invalidateQueries({ queryKey: ['map', 'markers'] })
}

const sortedMarkers = computed(() =>
  [...(markers.value ?? [])].sort((a, b) => a.sort_order - b.sort_order)
)
</script>

<template>
  <div>
    <div class="flex items-center justify-between">
      <h1 class="text-2xl font-bold text-gray-900">{{ t('mapAdmin.title') }}</h1>
      <button
        class="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700"
        @click="openCreate"
      >
        <Plus class="h-4 w-4" aria-hidden="true" />
        {{ t('mapAdmin.addMarker') }}
      </button>
    </div>

    <div v-if="isLoading" class="mt-8 text-center text-gray-500">{{ t('common.loading') }}</div>

    <div v-else-if="!sortedMarkers.length" class="mt-8 text-center text-gray-500">
      {{ t('mapAdmin.noMarkers') }}
    </div>

    <table v-else class="mt-6 w-full text-left text-sm">
      <thead class="border-b border-gray-200 text-gray-500">
        <tr>
          <th scope="col" class="pb-3 font-medium">{{ t('mapAdmin.markerType') }}</th>
          <th scope="col" class="pb-3 font-medium">{{ t('mapAdmin.label') }}</th>
          <th scope="col" class="pb-3 font-medium">{{ t('mapAdmin.lat') }}</th>
          <th scope="col" class="pb-3 font-medium">{{ t('mapAdmin.lng') }}</th>
          <th scope="col" class="pb-3 font-medium">{{ t('mapAdmin.sortOrder') }}</th>
          <th scope="col" class="pb-3 font-medium">{{ t('common.actions') }}</th>
        </tr>
      </thead>
      <tbody class="divide-y divide-gray-100">
        <tr v-for="m in sortedMarkers" :key="m.id">
          <td class="py-3">
            <span class="rounded-full bg-blue-100 px-2 py-0.5 text-xs font-medium text-blue-800">
              {{ t(`mapAdmin.types.${m.marker_type}`, m.marker_type) }}
            </span>
          </td>
          <td class="py-3 text-gray-900">{{ m.label || '—' }}</td>
          <td class="py-3 font-mono text-gray-600">{{ m.lat.toFixed(6) }}</td>
          <td class="py-3 font-mono text-gray-600">{{ m.lng.toFixed(6) }}</td>
          <td class="py-3 text-gray-600">{{ m.sort_order }}</td>
          <td class="py-3">
            <div class="flex gap-2">
              <button
                class="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-600"
                :aria-label="t('common.edit')"
                @click="openEdit(m)"
              >
                <Pencil class="h-4 w-4" />
              </button>
              <button
                class="rounded p-1 text-gray-400 hover:bg-red-50 hover:text-red-600"
                :aria-label="t('common.delete')"
                @click="deleteMarker(m)"
              >
                <Trash2 class="h-4 w-4" />
              </button>
            </div>
          </td>
        </tr>
      </tbody>
    </table>

    <div
      v-if="showModal"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      role="dialog"
      aria-modal="true"
    >
      <div class="w-full max-w-md rounded-xl bg-white p-6 shadow-xl">
        <h2 class="text-lg font-semibold text-gray-900">
          {{ editing ? t('mapAdmin.editMarker') : t('mapAdmin.addMarker') }}
        </h2>

        <form class="mt-4 space-y-4" @submit.prevent="save">
          <div>
            <label class="block text-sm font-medium text-gray-700">{{ t('mapAdmin.markerType') }}</label>
            <select v-model="form.marker_type" class="mt-1 block w-full rounded-lg border-gray-300 shadow-sm">
              <option v-for="mt in markerTypes" :key="mt" :value="mt">
                {{ t(`mapAdmin.types.${mt}`) }}
              </option>
            </select>
          </div>

          <div>
            <label class="block text-sm font-medium text-gray-700">{{ t('mapAdmin.label') }}</label>
            <input v-model="form.label" type="text" class="mt-1 block w-full rounded-lg border-gray-300 shadow-sm" />
          </div>

          <div class="grid grid-cols-2 gap-4">
            <div>
              <label class="block text-sm font-medium text-gray-700">{{ t('mapAdmin.lat') }}</label>
              <input v-model.number="form.lat" type="number" step="any" class="mt-1 block w-full rounded-lg border-gray-300 shadow-sm" />
            </div>
            <div>
              <label class="block text-sm font-medium text-gray-700">{{ t('mapAdmin.lng') }}</label>
              <input v-model.number="form.lng" type="number" step="any" class="mt-1 block w-full rounded-lg border-gray-300 shadow-sm" />
            </div>
          </div>

          <div>
            <label class="block text-sm font-medium text-gray-700">{{ t('mapAdmin.sortOrder') }}</label>
            <input v-model.number="form.sort_order" type="number" class="mt-1 block w-full rounded-lg border-gray-300 shadow-sm" />
          </div>

          <div class="flex justify-end gap-3 pt-2">
            <button
              type="button"
              class="rounded-lg border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
              @click="showModal = false"
            >
              {{ t('common.cancel') }}
            </button>
            <button
              type="submit"
              :disabled="saving"
              class="rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
            >
              {{ t('common.save') }}
            </button>
          </div>
        </form>
      </div>
    </div>
  </div>
</template>
