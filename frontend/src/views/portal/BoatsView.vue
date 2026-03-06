<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'
import { Plus, Pencil, Trash2 } from 'lucide-vue-next'

const { t } = useI18n()
const { fetchApi } = useApi()
const queryClient = useQueryClient()

interface Boat {
  id: string
  name: string
  type: string
  length: number | null
  beam: number | null
  draft: number | null
  registrationNumber: string
}

type BoatForm = Omit<Boat, 'id'>

const emptyForm = (): BoatForm => ({
  name: '',
  type: '',
  length: null,
  beam: null,
  draft: null,
  registrationNumber: '',
})

const showForm = ref(false)
const editingId = ref<string | null>(null)
const form = ref<BoatForm>(emptyForm())
const toast = ref<{ type: 'success' | 'error'; message: string } | null>(null)

function showToast(type: 'success' | 'error', message: string) {
  toast.value = { type, message }
  setTimeout(() => (toast.value = null), 3000)
}

const { data: boats, isLoading } = useQuery({
  queryKey: ['portal', 'boats'],
  queryFn: () => fetchApi<Boat[]>('/api/v1/members/me/boats'),
})

const { mutate: saveBoat, isPending: isSaving } = useMutation({
  mutationFn: () => {
    const url = editingId.value
      ? `/api/v1/members/me/boats/${editingId.value}`
      : '/api/v1/members/me/boats'
    const method = editingId.value ? 'PUT' : 'POST'
    return fetchApi<Boat>(url, { method, body: JSON.stringify(form.value) })
  },
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['portal', 'boats'] })
    showToast('success', t('portal.boats.saveSuccess'))
    closeForm()
  },
  onError: () => {
    showToast('error', t('portal.boats.saveError'))
  },
})

const { mutate: deleteBoat } = useMutation({
  mutationFn: (id: string) =>
    fetchApi(`/api/v1/members/me/boats/${id}`, { method: 'DELETE' }),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['portal', 'boats'] })
    showToast('success', t('portal.boats.deleteSuccess'))
  },
  onError: () => {
    showToast('error', t('portal.boats.deleteError'))
  },
})

function openAdd() {
  editingId.value = null
  form.value = emptyForm()
  showForm.value = true
}

function openEdit(boat: Boat) {
  editingId.value = boat.id
  form.value = {
    name: boat.name,
    type: boat.type,
    length: boat.length,
    beam: boat.beam,
    draft: boat.draft,
    registrationNumber: boat.registrationNumber,
  }
  showForm.value = true
}

function closeForm() {
  showForm.value = false
  editingId.value = null
  form.value = emptyForm()
}

function confirmDelete(id: string) {
  if (confirm(t('portal.boats.deleteConfirm'))) {
    deleteBoat(id)
  }
}
</script>

<template>
  <div>
    <div class="flex items-center justify-between">
      <h1 class="text-2xl font-bold text-gray-900">{{ t('portal.boats.title') }}</h1>
      <button
        v-if="!showForm"
        class="flex items-center gap-1.5 rounded-md bg-blue-600 px-3 py-2 text-sm font-semibold text-white shadow-sm hover:bg-blue-700"
        @click="openAdd"
      >
        <Plus class="h-4 w-4" />
        {{ t('portal.boats.addBoat') }}
      </button>
    </div>

    <div
      v-if="toast"
      :class="[
        'mt-4 rounded-md p-3 text-sm',
        toast.type === 'success' ? 'bg-green-50 text-green-800' : 'bg-red-50 text-red-800',
      ]"
    >
      {{ toast.message }}
    </div>

    <div v-if="isLoading" class="mt-6 text-gray-500">{{ t('common.loading') }}...</div>

    <template v-else>
      <form
        v-if="showForm"
        class="mt-6 max-w-lg space-y-4 rounded-lg border border-gray-200 bg-white p-5"
        @submit.prevent="saveBoat()"
      >
        <h2 class="text-lg font-semibold text-gray-900">
          {{ editingId ? t('portal.boats.editBoat') : t('portal.boats.addBoat') }}
        </h2>

        <div>
          <label for="boat-name" class="block text-sm font-medium text-gray-700">{{ t('portal.boats.name') }}</label>
          <input
            id="boat-name"
            v-model="form.name"
            type="text"
            required
            class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>

        <div>
          <label for="boat-type" class="block text-sm font-medium text-gray-700">{{ t('portal.boats.type') }}</label>
          <input
            id="boat-type"
            v-model="form.type"
            type="text"
            class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>

        <div class="grid grid-cols-3 gap-3">
          <div>
            <label for="boat-length" class="block text-sm font-medium text-gray-700">{{ t('portal.boats.length') }}</label>
            <input
              id="boat-length"
              v-model.number="form.length"
              type="number"
              step="0.1"
              min="0"
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>
          <div>
            <label for="boat-beam" class="block text-sm font-medium text-gray-700">{{ t('portal.boats.beam') }}</label>
            <input
              id="boat-beam"
              v-model.number="form.beam"
              type="number"
              step="0.1"
              min="0"
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>
          <div>
            <label for="boat-draft" class="block text-sm font-medium text-gray-700">{{ t('portal.boats.draft') }}</label>
            <input
              id="boat-draft"
              v-model.number="form.draft"
              type="number"
              step="0.1"
              min="0"
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>
        </div>

        <div>
          <label for="boat-reg" class="block text-sm font-medium text-gray-700">{{ t('portal.boats.registrationNumber') }}</label>
          <input
            id="boat-reg"
            v-model="form.registrationNumber"
            type="text"
            class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>

        <div class="flex gap-3">
          <button
            type="submit"
            :disabled="isSaving"
            class="rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white shadow-sm hover:bg-blue-700 disabled:opacity-50"
          >
            {{ t('common.save') }}
          </button>
          <button
            type="button"
            class="rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-semibold text-gray-700 shadow-sm hover:bg-gray-50"
            @click="closeForm"
          >
            {{ t('common.cancel') }}
          </button>
        </div>
      </form>

      <div v-if="!boats?.length && !showForm" class="mt-6 text-gray-500">
        {{ t('portal.boats.noBoats') }}
      </div>

      <div v-else-if="boats?.length" class="mt-6 overflow-x-auto">
        <table class="min-w-full divide-y divide-gray-200">
          <thead class="bg-gray-50">
            <tr>
              <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('portal.boats.name') }}</th>
              <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('portal.boats.type') }}</th>
              <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('portal.boats.length') }}</th>
              <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('portal.boats.beam') }}</th>
              <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('portal.boats.draft') }}</th>
              <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('portal.boats.registrationNumber') }}</th>
              <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('common.actions') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-200 bg-white">
            <tr v-for="boat in boats" :key="boat.id">
              <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-900">{{ boat.name }}</td>
              <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ boat.type }}</td>
              <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ boat.length ?? '—' }}</td>
              <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ boat.beam ?? '—' }}</td>
              <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ boat.draft ?? '—' }}</td>
              <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ boat.registrationNumber || '—' }}</td>
              <td class="whitespace-nowrap px-4 py-3 text-sm">
                <div class="flex gap-2">
                  <button
                    class="text-blue-600 hover:text-blue-800"
                    :title="t('common.edit')"
                    @click="openEdit(boat)"
                  >
                    <Pencil class="h-4 w-4" />
                  </button>
                  <button
                    class="text-red-600 hover:text-red-800"
                    :title="t('common.delete')"
                    @click="confirmDelete(boat.id)"
                  >
                    <Trash2 class="h-4 w-4" />
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>
  </div>
</template>
