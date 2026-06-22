<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'
import { Plus } from 'lucide-vue-next'
import BoatForm, { type BoatFormValue } from '@/components/boats/BoatForm.vue'
import BoatCard from '@/components/boats/BoatCard.vue'
import type { components } from '@/types/api'

type Boat = components['schemas']['Boat']

const { t } = useI18n()
const client = useApiClient()
const queryClient = useQueryClient()

const showForm = ref(false)
const editingId = ref<string | null>(null)
const editingBoat = ref<Boat | null>(null)
const toast = ref<{ type: 'success' | 'error'; message: string } | null>(null)
const formError = ref<string | null>(null)

function showToast(type: 'success' | 'error', message: string) {
  toast.value = { type, message }
  setTimeout(() => (toast.value = null), 3000)
}

const { data: boats, isLoading } = useQuery({
  queryKey: ['portal', 'boats'],
  queryFn: async () => unwrap(await client.GET('/api/v1/members/me/boats')),
})

const { mutate: saveBoat, isPending: isSaving } = useMutation({
  mutationFn: async (value: BoatFormValue) => {
    if (editingId.value) {
      return unwrap(await client.PUT('/api/v1/members/me/boats/{boatID}', {
        params: { path: { boatID: editingId.value } },
        body: value as any,
      }))
    }
    return unwrap(await client.POST('/api/v1/members/me/boats', {
      body: value as any,
    }))
  },
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['portal', 'boats'] })
    showToast('success', t('portal.boats.saveSuccess'))
    closeForm()
  },
  onError: (err: any) => {
    formError.value = err?.message ?? t('portal.boats.saveError')
    showToast('error', t('portal.boats.saveError'))
  },
})

const { mutate: deleteBoat } = useMutation({
  mutationFn: async (id: string) =>
    unwrap(await client.DELETE('/api/v1/members/me/boats/{boatID}', { params: { path: { boatID: id } } })),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['portal', 'boats'] })
    showToast('success', t('portal.boats.deleteSuccess'))
  },
  onError: () => showToast('error', t('portal.boats.deleteError')),
})

function openAdd() {
  editingId.value = null
  editingBoat.value = null
  formError.value = null
  showForm.value = true
}

function openEdit(boat: Boat) {
  editingId.value = boat.id
  editingBoat.value = boat
  formError.value = null
  showForm.value = true
}

function closeForm() {
  showForm.value = false
  editingId.value = null
  editingBoat.value = null
  formError.value = null
}

function confirmDelete(id: string) {
  if (confirm(t('portal.boats.deleteConfirm'))) deleteBoat(id)
}
</script>

<template>
  <div>
    <div class="flex items-center justify-between">
      <h1 class="text-2xl font-bold text-gray-900">{{ t('portal.boats.title') }}</h1>
      <button
        v-if="!showForm"
        class="flex items-center gap-1.5 rounded-md bg-brand-600 px-3 py-2 text-sm font-semibold text-white shadow-sm hover:bg-brand-700"
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
      <div v-if="showForm" class="mt-6 max-w-lg rounded-lg border border-gray-200 bg-white p-5">
        <h2 class="mb-4 text-lg font-semibold text-gray-900">
          {{ editingId ? t('portal.boats.editBoat') : t('portal.boats.addBoat') }}
        </h2>
        <BoatForm
          :initial="editingBoat ?? undefined"
          :editing="!!editingId"
          :confirmed="editingBoat?.measurements_confirmed"
          :saving="isSaving"
          :error="formError"
          @submit="(v) => saveBoat(v)"
          @cancel="closeForm"
        />
      </div>

      <div v-if="!boats?.length && !showForm" class="mt-6 text-gray-500">
        {{ t('portal.boats.noBoats') }}
      </div>

      <div v-else-if="boats?.length" class="mt-6 space-y-3">
        <BoatCard
          v-for="boat in boats"
          :key="boat.id"
          :boat="boat"
          actions
          @edit="(b) => openEdit(b as Boat)"
          @delete="confirmDelete"
        />
      </div>
    </template>
  </div>
</template>
