<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'
import { Plus, Pencil, Trash2, X } from 'lucide-vue-next'

const { t } = useI18n()
const { fetchApi } = useApi()
const queryClient = useQueryClient()

import type { components } from '@/types/api'

type Product = components['schemas']['Product']

const { data: response, isLoading } = useQuery({
  queryKey: ['admin', 'products'],
  queryFn: () => fetchApi<{ products: Product[] }>('/api/v1/admin/products'),
})

interface FormData {
  id?: string
  name: string
  description: string
  price: string
  image_url: string
  stock: string
  sort_order: string
  is_active: boolean
}

const emptyForm: FormData = {
  name: '',
  description: '',
  price: '',
  image_url: '',
  stock: '0',
  sort_order: '0',
  is_active: true,
}

const showForm = ref(false)
const form = ref<FormData>({ ...emptyForm })

function openCreate() {
  form.value = { ...emptyForm }
  showForm.value = true
}

function openEdit(p: Product) {
  form.value = {
    id: p.id,
    name: p.name,
    description: p.description,
    price: String(p.price),
    image_url: p.image_url,
    stock: String(p.stock),
    sort_order: String(p.sort_order),
    is_active: p.is_active,
  }
  showForm.value = true
}

const { mutate: saveProduct, isPending: isSaving } = useMutation({
  mutationFn: () => {
    const payload = {
      name: form.value.name,
      description: form.value.description,
      price: parseFloat(form.value.price) || 0,
      image_url: form.value.image_url,
      stock: parseInt(form.value.stock) || 0,
      sort_order: parseInt(form.value.sort_order) || 0,
      is_active: form.value.is_active,
    }
    if (form.value.id) {
      return fetchApi(`/api/v1/admin/products/${form.value.id}`, {
        method: 'PUT',
        body: JSON.stringify(payload),
      })
    }
    return fetchApi('/api/v1/admin/products', {
      method: 'POST',
      body: JSON.stringify(payload),
    })
  },
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['admin', 'products'] })
    queryClient.invalidateQueries({ queryKey: ['products'] })
    showForm.value = false
  },
})

const { mutate: deleteProduct } = useMutation({
  mutationFn: (id: string) =>
    fetchApi(`/api/v1/admin/products/${id}`, { method: 'DELETE' }),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['admin', 'products'] })
    queryClient.invalidateQueries({ queryKey: ['products'] })
  },
})

function confirmDelete(id: string) {
  if (confirm(t('admin.products.deleteConfirm'))) {
    deleteProduct(id)
  }
}
</script>

<template>
  <div>
    <div class="flex items-center justify-between">
      <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.products.title') }}</h1>
      <button
        v-if="!showForm"
        class="flex items-center gap-1.5 rounded-md bg-blue-600 px-3 py-2 text-sm font-semibold text-white shadow-sm hover:bg-blue-700"
        @click="openCreate"
      >
        <Plus class="h-4 w-4" />
        {{ t('admin.products.newProduct') }}
      </button>
    </div>

    <form
      v-if="showForm"
      class="mt-6 max-w-2xl space-y-4 rounded-lg border border-gray-200 bg-white p-5"
      @submit.prevent="saveProduct()"
    >
      <div class="flex items-center justify-between">
        <h2 class="text-lg font-semibold text-gray-900">
          {{ form.id ? t('admin.products.editProduct') : t('admin.products.newProduct') }}
        </h2>
        <button type="button" class="text-gray-400 hover:text-gray-600" @click="showForm = false">
          <X class="h-5 w-5" />
        </button>
      </div>

      <div>
        <label class="block text-sm font-medium text-gray-700">{{ t('admin.products.name') }}</label>
        <input
          v-model="form.name"
          type="text"
          required
          class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        />
      </div>

      <div>
        <label class="block text-sm font-medium text-gray-700">{{ t('admin.products.description') }}</label>
        <textarea
          v-model="form.description"
          rows="2"
          class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        />
      </div>

      <div class="grid grid-cols-3 gap-4">
        <div>
          <label class="block text-sm font-medium text-gray-700">{{ t('admin.products.price') }}</label>
          <input
            v-model="form.price"
            type="number"
            step="1"
            min="0"
            required
            class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700">{{ t('admin.products.stock') }}</label>
          <input
            v-model="form.stock"
            type="number"
            min="0"
            class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700">{{ t('admin.products.sortOrder') }}</label>
          <input
            v-model="form.sort_order"
            type="number"
            class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>
      </div>

      <div>
        <label class="block text-sm font-medium text-gray-700">{{ t('admin.products.imageUrl') }}</label>
        <input
          v-model="form.image_url"
          type="text"
          placeholder="https://..."
          class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        />
      </div>

      <label class="flex items-center gap-2 text-sm text-gray-700">
        <input v-model="form.is_active" type="checkbox" class="rounded border-gray-300" />
        {{ t('admin.products.activeCheckbox') }}
      </label>

      <div class="flex gap-3 pt-2">
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
          @click="showForm = false"
        >
          {{ t('common.cancel') }}
        </button>
      </div>
    </form>

    <div v-if="isLoading" class="mt-6 text-gray-500">{{ t('common.loading') }}...</div>

    <div v-else class="mt-6 overflow-x-auto">
      <table class="min-w-full divide-y divide-gray-200">
        <thead class="bg-gray-50">
          <tr>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.products.product') }}</th>
            <th scope="col" class="px-4 py-3 text-right text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.products.price') }}</th>
            <th scope="col" class="px-4 py-3 text-right text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.products.stock') }}</th>
            <th scope="col" class="px-4 py-3 text-center text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.products.status') }}</th>
            <th scope="col" class="px-4 py-3 text-right text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('common.actions') }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 bg-white">
          <tr v-if="!response?.products?.length">
            <td colspan="5" class="px-4 py-8 text-center text-gray-500">{{ t('admin.products.noProducts') }}</td>
          </tr>
          <tr v-for="p in response?.products" :key="p.id">
            <td class="px-4 py-3 text-sm">
              <div class="font-medium text-gray-900">{{ p.name }}</div>
              <div v-if="p.description" class="text-xs text-gray-500">{{ p.description }}</div>
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-right text-sm font-medium text-gray-900">
              {{ p.price.toLocaleString('nb-NO') }} kr
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-right text-sm">
              <span :class="p.stock <= 5 ? 'text-amber-600 font-medium' : 'text-gray-500'">
                {{ p.stock }}
              </span>
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-center text-sm">
              <span
                :class="[
                  'rounded-full px-2 py-0.5 text-xs font-medium',
                  p.is_active ? 'bg-green-100 text-green-800' : 'bg-gray-100 text-gray-500',
                ]"
              >
                {{ p.is_active ? t('admin.products.active') : t('admin.products.inactive') }}
              </span>
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-right text-sm">
              <button class="mr-2 text-gray-500 hover:text-blue-600" @click="openEdit(p)">
                <Pencil class="h-4 w-4" />
              </button>
              <button class="text-gray-500 hover:text-red-600" @click="confirmDelete(p.id)">
                <Trash2 class="h-4 w-4" />
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
