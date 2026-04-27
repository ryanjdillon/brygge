<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'
import { sortBySlip } from '@/lib/slipSort'
import { useAuthStore } from '@/stores/auth'
import { useTotpGateStore } from '@/stores/totpGate'
import { Plus, Pencil, Trash2 } from 'lucide-vue-next'
const { t } = useI18n()
const client = useApiClient()
const queryClient = useQueryClient()
const auth = useAuthStore()
const totpGate = useTotpGateStore()

async function ensureFreshTotp(): Promise<boolean> {
  if (auth.hasFreshTotp) return true
  return totpGate.open()
}

type Slip = {
  id: string
  number: string
  section: string
  length_m?: number | null
  width_m?: number | null
  depth_m?: number | null
  status: string
  notes?: string | null
  occupant_name?: string | null
}

const { data: slipsResponse, isLoading, isError } = useQuery({
  queryKey: ['admin', 'slips'],
  queryFn: async () => {
    const res = unwrap(await client.GET('/api/v1/admin/slips'))
    return (res.items ?? []) as Slip[]
  },
})

const dockFilter = ref<string>('')

const allDocks = computed<string[]>(() => {
  const set = new Set<string>()
  for (const s of slipsResponse.value ?? []) {
    if (s.section) set.add(s.section)
  }
  return [...set].sort((a, b) => a.localeCompare(b, undefined, { numeric: true }))
})

const slips = computed<Slip[]>(() => {
  const all = slipsResponse.value ?? []
  const filtered = dockFilter.value ? all.filter((s) => s.section === dockFilter.value) : all
  return sortBySlip(filtered)
})

const showCreateForm = ref(false)
const editingSlip = ref<Slip | null>(null)
const deletingSlip = ref<Slip | null>(null)

const createForm = ref({ number: '', section: '', length_m: '', width_m: '', depth_m: '' })
const editForm = ref({ number: '', section: '', length_m: '', width_m: '', depth_m: '' })

const submitError = ref<string | null>(null)
const deleteError = ref<string | null>(null)

const { mutate: createSlip, isPending: isCreating } = useMutation({
  mutationFn: async () =>
    unwrap(await client.POST('/api/v1/admin/slips', {
      body: {
        number: createForm.value.number,
        section: createForm.value.section,
        length_m: createForm.value.length_m ? parseFloat(createForm.value.length_m) : null,
        width_m: createForm.value.width_m ? parseFloat(createForm.value.width_m) : null,
        depth_m: createForm.value.depth_m ? parseFloat(createForm.value.depth_m) : null,
        notes: createForm.value.notes,
      } as any,
    })),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['admin', 'slips'] })
    showCreateForm.value = false
    submitError.value = null
    createForm.value = { number: '', section: '', length_m: '', width_m: '', depth_m: '', notes: '' }
  },
  onError: (err: unknown) => {
    submitError.value = err instanceof Error ? err.message : String(err)
  },
})

const { mutate: updateSlip, isPending: isUpdating } = useMutation({
  mutationFn: async () => {
    const slip = editingSlip.value
    if (!slip) throw new Error('no slip selected')
    return unwrap(await client.PUT('/api/v1/admin/slips/{slipID}', {
      params: { path: { slipID: slip.id } },
      body: {
        number: editForm.value.number,
        section: editForm.value.section,
        length_m: editForm.value.length_m ? parseFloat(editForm.value.length_m) : null,
        width_m: editForm.value.width_m ? parseFloat(editForm.value.width_m) : null,
        depth_m: editForm.value.depth_m ? parseFloat(editForm.value.depth_m) : null,
        notes: editForm.value.notes,
      } as any,
    }))
  },
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['admin', 'slips'] })
    editingSlip.value = null
    submitError.value = null
  },
  onError: (err: unknown) => {
    submitError.value = err instanceof Error ? err.message : String(err)
  },
})

const { mutate: deleteSlip, isPending: isDeleting } = useMutation({
  mutationFn: async () => {
    const slip = deletingSlip.value
    if (!slip) throw new Error('no slip selected')
    return unwrap(await client.DELETE('/api/v1/admin/slips/{slipID}', {
      params: { path: { slipID: slip.id } },
    }))
  },
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['admin', 'slips'] })
    deletingSlip.value = null
    deleteError.value = null
  },
  onError: (err: unknown) => {
    deleteError.value = err instanceof Error ? err.message : String(err)
  },
})

// Pre-flight TOTP at click time so the user can cancel before any
// destructive request fires (a post-submit prompt would already have
// attempted the action server-side).
async function openCreate() {
  if (!(await ensureFreshTotp())) return
  submitError.value = null
  showCreateForm.value = true
}

async function openEdit(slip: Slip) {
  if (!(await ensureFreshTotp())) return
  editingSlip.value = slip
  editForm.value = {
    number: slip.number ?? '',
    section: slip.section ?? '',
    length_m: slip.length_m != null ? String(slip.length_m) : '',
    width_m: slip.width_m != null ? String(slip.width_m) : '',
    depth_m: slip.depth_m != null ? String(slip.depth_m) : '',
    notes: slip.notes ?? '',
  }
  submitError.value = null
}

function closeEdit() {
  editingSlip.value = null
  submitError.value = null
}

async function openDelete(slip: Slip) {
  if (!(await ensureFreshTotp())) return
  deletingSlip.value = slip
  deleteError.value = null
}

function closeDelete() {
  deletingSlip.value = null
  deleteError.value = null
}
</script>

<template>
  <div>
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.sidebar.slips') }}</h1>
      <div class="flex items-center gap-3">
        <label class="flex items-center gap-2 text-sm text-gray-700">
          <span class="font-medium">{{ t('admin.slips.dockFilterLabel') }}:</span>
          <select v-model="dockFilter" class="rounded-md border border-gray-300 px-2 py-1.5 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500">
            <option value="">{{ t('admin.slips.dockFilterAll') }}</option>
            <option v-for="d in allDocks" :key="d" :value="d">{{ d }}</option>
          </select>
        </label>
        <button
          v-if="!showCreateForm"
          class="flex items-center gap-1.5 rounded-md bg-blue-600 px-3 py-2 text-sm font-semibold text-white shadow-sm hover:bg-blue-700"
          @click="openCreate"
        >
          <Plus class="h-4 w-4" />
          {{ t('admin.slips.newSlip') }}
        </button>
      </div>
    </div>

    <form
      v-if="showCreateForm"
      class="mt-6 max-w-lg space-y-4 rounded-lg border border-gray-200 bg-white p-5"
      @submit.prevent="createSlip()"
    >
      <div class="grid grid-cols-2 gap-3">
        <div>
          <label class="block text-sm font-medium text-gray-700">{{ t('admin.slips.section') }}</label>
          <input v-model="createForm.section" type="text" required class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500" />
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700">{{ t('admin.slips.number') }}</label>
          <input v-model="createForm.number" type="text" required class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500" />
        </div>
      </div>
      <div class="grid grid-cols-3 gap-3">
        <div>
          <label class="block text-sm font-medium text-gray-700">{{ t('admin.slips.length') }} <span class="text-xs font-normal text-gray-400">({{ t('common.optional') }})</span></label>
          <input v-model="createForm.length_m" type="number" step="0.1" class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500" />
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700">{{ t('admin.slips.width') }} <span class="text-xs font-normal text-gray-400">({{ t('common.optional') }})</span></label>
          <input v-model="createForm.width_m" type="number" step="0.1" class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500" />
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700">{{ t('admin.slips.depth') }} <span class="text-xs font-normal text-gray-400">({{ t('common.optional') }})</span></label>
          <input v-model="createForm.depth_m" type="number" step="0.1" class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500" />
        </div>
      </div>
      <div>
        <label class="block text-sm font-medium text-gray-700">{{ t('admin.slips.notes') }} <span class="text-xs font-normal text-gray-400">({{ t('common.optional') }})</span></label>
        <textarea v-model="createForm.notes" rows="3" class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500" />
      </div>
      <div v-if="submitError" class="rounded-md bg-red-50 p-3 text-sm text-red-800">{{ submitError }}</div>
      <div class="flex gap-3">
        <button type="submit" :disabled="isCreating" class="rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white shadow-sm hover:bg-blue-700 disabled:opacity-50">{{ t('common.save') }}</button>
        <button type="button" class="rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-semibold text-gray-700 shadow-sm hover:bg-gray-50" @click="showCreateForm = false">{{ t('common.cancel') }}</button>
      </div>
    </form>

    <div v-if="isLoading" class="mt-6 text-gray-500">{{ t('common.loading') }}...</div>
    <div v-else-if="isError" class="mt-6 rounded-md bg-red-50 p-3 text-sm text-red-800">{{ t('admin.slips.loadError') }}</div>

    <div v-else class="mt-6 overflow-x-auto">
      <table class="min-w-full divide-y divide-gray-200">
        <thead class="bg-gray-50">
          <tr>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.slips.section') }}</th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.slips.number') }}</th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.slips.size') }}</th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.slips.status') }}</th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.slips.assignee') }}</th>
            <th scope="col" class="px-4 py-3 text-right text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('common.actions') }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 bg-white">
          <tr v-for="slip in slips" :key="slip.id">
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ slip.section }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-sm font-medium text-gray-900">#{{ slip.number }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ slip.length_m ?? '—' }} × {{ slip.width_m ?? '—' }} m</td>
            <td class="whitespace-nowrap px-4 py-3 text-sm">
              <span :class="['rounded-full px-2.5 py-0.5 text-xs font-medium', slip.status === 'vacant' ? 'bg-green-100 text-green-800' : 'bg-blue-100 text-blue-800']">
                {{ slip.status }}
              </span>
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ slip.occupant_name ?? '—' }}</td>
            <td class="max-w-xs truncate px-4 py-3 text-sm text-gray-500" :title="slip.notes ?? ''">{{ slip.notes || '—' }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-right text-sm">
              <div class="flex justify-end gap-2">
                <button
                  type="button"
                  class="inline-flex items-center gap-1 rounded-md border border-gray-300 bg-white px-2.5 py-1.5 text-xs font-semibold text-gray-700 shadow-sm hover:bg-gray-50"
                  :aria-label="t('common.edit')"
                  @click="openEdit(slip)"
                >
                  <Pencil class="h-3.5 w-3.5" />
                  {{ t('common.edit') }}
                </button>
                <button
                  type="button"
                  class="inline-flex items-center gap-1 rounded-md border border-red-300 bg-white px-2.5 py-1.5 text-xs font-semibold text-red-700 shadow-sm hover:bg-red-50"
                  :aria-label="t('common.delete')"
                  @click="openDelete(slip)"
                >
                  <Trash2 class="h-3.5 w-3.5" />
                  {{ t('common.delete') }}
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div
      v-if="editingSlip"
      role="dialog"
      aria-modal="true"
      class="fixed inset-0 z-40 flex items-center justify-center bg-black/40 p-4"
      @click.self="closeEdit"
    >
      <form
        class="w-full max-w-lg space-y-4 rounded-lg border border-gray-200 bg-white p-5 shadow-xl"
        @submit.prevent="updateSlip()"
      >
        <h2 class="text-lg font-semibold text-gray-900">{{ t('admin.slips.editTitle', { number: editingSlip.number }) }}</h2>
        <div class="grid grid-cols-2 gap-3">
          <div>
            <label class="block text-sm font-medium text-gray-700">{{ t('admin.slips.section') }}</label>
            <input v-model="editForm.section" type="text" required class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500" />
          </div>
          <div>
            <label class="block text-sm font-medium text-gray-700">{{ t('admin.slips.number') }}</label>
            <input v-model="editForm.number" type="text" required class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500" />
          </div>
        </div>
        <div class="grid grid-cols-3 gap-3">
          <div>
            <label class="block text-sm font-medium text-gray-700">{{ t('admin.slips.length') }} <span class="text-xs font-normal text-gray-400">({{ t('common.optional') }})</span></label>
            <input v-model="editForm.length_m" type="number" step="0.1" class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500" />
          </div>
          <div>
            <label class="block text-sm font-medium text-gray-700">{{ t('admin.slips.width') }} <span class="text-xs font-normal text-gray-400">({{ t('common.optional') }})</span></label>
            <input v-model="editForm.width_m" type="number" step="0.1" class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500" />
          </div>
          <div>
            <label class="block text-sm font-medium text-gray-700">{{ t('admin.slips.depth') }} <span class="text-xs font-normal text-gray-400">({{ t('common.optional') }})</span></label>
            <input v-model="editForm.depth_m" type="number" step="0.1" class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500" />
          </div>
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700">{{ t('admin.slips.notes') }} <span class="text-xs font-normal text-gray-400">({{ t('common.optional') }})</span></label>
          <textarea v-model="editForm.notes" rows="3" class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500" />
        </div>
        <div v-if="submitError" class="rounded-md bg-red-50 p-3 text-sm text-red-800">{{ submitError }}</div>
        <div class="flex justify-end gap-3">
          <button type="button" class="rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-semibold text-gray-700 shadow-sm hover:bg-gray-50" @click="closeEdit">{{ t('common.cancel') }}</button>
          <button type="submit" :disabled="isUpdating" class="rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white shadow-sm hover:bg-blue-700 disabled:opacity-50">{{ t('common.save') }}</button>
        </div>
      </form>
    </div>

    <div
      v-if="deletingSlip"
      role="dialog"
      aria-modal="true"
      class="fixed inset-0 z-40 flex items-center justify-center bg-black/40 p-4"
      @click.self="closeDelete"
    >
      <div class="w-full max-w-md space-y-4 rounded-lg border border-gray-200 bg-white p-5 shadow-xl">
        <h2 class="text-lg font-semibold text-gray-900">{{ t('admin.slips.deleteTitle') }}</h2>
        <p class="text-sm text-gray-600">{{ t('admin.slips.deleteConfirm', { number: deletingSlip.number }) }}</p>
        <div v-if="deleteError" class="rounded-md bg-red-50 p-3 text-sm text-red-800">{{ deleteError }}</div>
        <div class="flex justify-end gap-3">
          <button type="button" class="rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-semibold text-gray-700 shadow-sm hover:bg-gray-50" @click="closeDelete">{{ t('common.cancel') }}</button>
          <button type="button" :disabled="isDeleting" class="rounded-md bg-red-600 px-4 py-2 text-sm font-semibold text-white shadow-sm hover:bg-red-700 disabled:opacity-50" @click="deleteSlip()">{{ t('common.delete') }}</button>
        </div>
      </div>
    </div>
  </div>
</template>
