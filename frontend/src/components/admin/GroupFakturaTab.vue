<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Search, Receipt, X } from 'lucide-vue-next'
import BulkInvoicesModal from '@/components/admin/BulkInvoicesModal.vue'
import { useRangeSelect } from '@/composables/useRangeSelect'
import DockFilter from '@/components/admin/DockFilter.vue'
import SpotFilter, { type SpotFilterValue } from '@/components/admin/SpotFilter.vue'
import NotesFilter, { type NotesFilterValue } from '@/components/admin/NotesFilter.vue'

interface Row {
  id: string
  full_name: string
  email: string
  phone?: string
  has_active_slip?: boolean
}

const { t } = useI18n()

const search = ref('')
const debouncedSearch = ref('')
const spotFilter = ref<SpotFilterValue>('')
const dockFilter = ref<string>('')
const notesFilter = ref<NotesFilterValue>('')
const rows = ref<Row[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const selected = ref<Set<string>>(new Set())
const showBulkModal = ref(false)
const dockOptions = ref<string[]>([])

let debounceHandle: ReturnType<typeof setTimeout> | null = null
watch(search, (v) => {
  if (debounceHandle) clearTimeout(debounceHandle)
  debounceHandle = setTimeout(() => {
    debouncedSearch.value = v.trim()
  }, 250)
})

watch([debouncedSearch, spotFilter, dockFilter, notesFilter], load, { immediate: false })

async function load() {
  loading.value = true
  error.value = null
  try {
    const params = new URLSearchParams()
    if (debouncedSearch.value) params.set('q', debouncedSearch.value)
    if (spotFilter.value) params.set('spot', spotFilter.value)
    if (dockFilter.value) params.set('dock', dockFilter.value)
    if (notesFilter.value) params.set('notes', notesFilter.value)
    params.set('limit', '200')
    const res = await fetch(`/api/v1/admin/users?${params.toString()}`, { credentials: 'include' })
    if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
    const body = await res.json()
    const items: Row[] = (body.items ?? body.users ?? body ?? []).map((u: Record<string, unknown>) => ({
      id: String(u.id),
      full_name: String(u.full_name ?? `${u.first_name ?? ''} ${u.last_name ?? ''}`).trim(),
      email: String(u.email ?? ''),
      phone: u.phone ? String(u.phone) : undefined,
      has_active_slip: Boolean(u.has_active_slip ?? u.active_slip ?? false),
    }))
    rows.value = items
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    loading.value = false
  }
}

async function loadDockOptions() {
  try {
    const res = await fetch('/api/v1/admin/slips?limit=500', { credentials: 'include' })
    if (!res.ok) return
    const body = await res.json()
    const sections = new Set<string>()
    for (const s of (body.items ?? body.data ?? [])) {
      if (s.section) sections.add(String(s.section))
    }
    dockOptions.value = [...sections].sort((a, b) => a.localeCompare(b, undefined, { numeric: true }))
  } catch {
    // Silent — the dock filter just stays empty if slips can't load.
  }
}

onMounted(() => {
  load()
  loadDockOptions()
})

const allSelected = computed(() => rows.value.length > 0 && rows.value.every((r) => selected.value.has(r.id)))

const { onCheckboxClick: onRowClick, resetAnchor } = useRangeSelect(selected, () => rows.value)

function toggleAll() {
  const next = new Set(selected.value)
  if (allSelected.value) for (const r of rows.value) next.delete(r.id)
  else for (const r of rows.value) next.add(r.id)
  selected.value = next
  resetAnchor()
}
function clearSelection() {
  selected.value = new Set()
  resetAnchor()
}

const selectedIds = computed(() => [...selected.value])
const userNamesById = computed(() => {
  const out: Record<string, string> = {}
  for (const r of rows.value) out[r.id] = r.full_name
  return out
})

function openBulk() {
  if (selected.value.size === 0) return
  showBulkModal.value = true
}

const emit = defineEmits<{ (e: 'completed'): void }>()

function onBulkCompleted() {
  showBulkModal.value = false
  selected.value = new Set()
  emit('completed')
}
</script>

<template>
  <div>
    <div class="flex flex-wrap items-center gap-2">
      <div class="relative">
        <Search class="pointer-events-none absolute left-3 top-2.5 h-4 w-4 text-gray-400" />
        <input
          v-model="search"
          type="search"
          :placeholder="t('admin.groupFaktura.searchPlaceholder')"
          class="rounded-md border border-gray-300 py-2 pl-9 pr-3 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        />
      </div>
      <DockFilter id="group-faktura-dock-filter" v-model="dockFilter" :options="dockOptions" />
      <SpotFilter id="group-faktura-spot-filter" v-model="spotFilter" />
      <NotesFilter id="group-faktura-notes-filter" v-model="notesFilter" />
      <span class="ml-auto text-xs text-gray-500">
        {{ t('admin.groupFaktura.summary', { n: rows.length, sel: selected.size }) }}
      </span>
    </div>

    <div
      v-if="selected.size > 0"
      class="mt-3 flex items-center gap-3 rounded-md border border-blue-200 bg-blue-50 px-3 py-2 text-sm"
    >
      <span class="font-medium text-blue-900">
        {{ t('admin.groupFaktura.selectedCount', { n: selected.size }) }}
      </span>
      <button
        type="button"
        class="inline-flex items-center gap-1 rounded text-xs text-blue-700 hover:text-blue-900 hover:underline"
        :title="t('common.clearSelection')"
        @click="clearSelection"
      >
        <X class="h-3 w-3" />
        {{ t('common.clearSelection') }}
      </button>
      <button
        class="ml-auto inline-flex items-center gap-1 rounded-md bg-blue-600 px-3 py-1 text-xs font-semibold text-white hover:bg-blue-700"
        @click="openBulk"
      >
        <Receipt class="h-3.5 w-3.5" /> {{ t('admin.groupFaktura.create') }}
      </button>
    </div>

    <p v-if="loading" class="mt-4 text-sm text-gray-500">{{ t('common.loading') }}…</p>
    <p v-else-if="error" class="mt-4 rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">{{ error }}</p>

    <div v-else class="mt-3 overflow-x-auto">
      <table class="min-w-full divide-y divide-gray-200">
        <thead class="bg-gray-50">
          <tr>
            <th class="w-8 px-2 py-2 text-center">
              <input type="checkbox" :checked="allSelected" class="rounded border-gray-300" @change="toggleAll" />
            </th>
            <th class="px-3 py-2 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.groupFaktura.member') }}</th>
            <th class="px-3 py-2 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.groupFaktura.email') }}</th>
            <th class="px-3 py-2 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.groupFaktura.slip') }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 bg-white">
          <tr v-for="(r, idx) in rows" :key="r.id" :class="{ 'bg-blue-50/50': selected.has(r.id) }">
            <td class="px-2 py-2 text-center">
              <input type="checkbox" :checked="selected.has(r.id)" class="rounded border-gray-300" @click="onRowClick(idx, $event)" />
            </td>
            <td class="px-3 py-2 text-sm font-medium text-gray-900">{{ r.full_name }}</td>
            <td class="px-3 py-2 text-sm text-gray-700">{{ r.email }}</td>
            <td class="px-3 py-2 text-sm text-gray-600">{{ r.has_active_slip ? '✓' : '—' }}</td>
          </tr>
        </tbody>
      </table>
      <p v-if="rows.length === 0" class="mt-6 text-center text-sm text-gray-500">{{ t('admin.groupFaktura.empty') }}</p>
    </div>

    <BulkInvoicesModal
      v-if="showBulkModal"
      :user-ids="selectedIds"
      :user-names-by-id="userNamesById"
      @close="showBulkModal = false"
      @completed="onBulkCompleted"
    />
  </div>
</template>
