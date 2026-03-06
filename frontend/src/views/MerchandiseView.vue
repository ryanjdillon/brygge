<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useQuery } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'
import { useCartStore } from '@/stores/cart'
import { ShoppingCart, Plus, Check } from 'lucide-vue-next'
import { ref } from 'vue'

const { t } = useI18n()
const { fetchApi } = useApi()
const cart = useCartStore()

interface Product {
  id: string
  name: string
  description: string
  price: number
  currency: string
  image_url: string
  stock: number
}

const { data: response, isLoading } = useQuery({
  queryKey: ['products'],
  queryFn: () => fetchApi<{ products: Product[] }>('/api/v1/products'),
})

const addedIds = ref<Set<string>>(new Set())

function addToCart(product: Product) {
  cart.addItem({
    id: product.id,
    type: 'product',
    name: product.name,
    unitPrice: product.price,
    imageUrl: product.image_url,
  })
  addedIds.value.add(product.id)
  setTimeout(() => addedIds.value.delete(product.id), 1500)
}

function cartQuantity(productId: string): number {
  return cart.items.find((i) => i.id === productId)?.quantity ?? 0
}
</script>

<template>
  <div class="mx-auto max-w-7xl px-4 py-12 sm:px-6 lg:px-8">
    <div class="flex items-center justify-between">
      <h1 class="text-3xl font-bold text-gray-900">{{ t('merchandise.title') }}</h1>
      <router-link
        v-if="cart.totalItems > 0"
        to="/checkout"
        class="flex items-center gap-2 rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white shadow-sm hover:bg-blue-700"
      >
        <ShoppingCart class="h-4 w-4" />
        {{ t('merchandise.cart') }} ({{ cart.totalItems }})
      </router-link>
    </div>

    <div v-if="isLoading" class="mt-10 grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
      <div v-for="i in 3" :key="i" class="animate-pulse rounded-lg border border-gray-200 p-6">
        <div class="h-40 rounded-md bg-gray-200" />
        <div class="mt-4 h-5 w-32 rounded bg-gray-200" />
        <div class="mt-2 h-4 w-20 rounded bg-gray-200" />
      </div>
    </div>

    <div v-else-if="!response?.products?.length" class="mt-10 text-center text-gray-500">
      Ingen produkter tilgjengelig ennå
    </div>

    <div v-else class="mt-10 grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
      <div
        v-for="product in response.products"
        :key="product.id"
        class="flex flex-col rounded-lg border border-gray-200 bg-white"
      >
        <div class="flex h-48 items-center justify-center rounded-t-lg bg-gray-50 text-gray-300">
          <ShoppingCart class="h-16 w-16" />
        </div>
        <div class="flex flex-1 flex-col p-5">
          <h3 class="text-lg font-semibold text-gray-900">{{ product.name }}</h3>
          <p class="mt-1 flex-1 text-sm text-gray-500">{{ product.description }}</p>
          <div class="mt-4 flex items-center justify-between">
            <span class="text-xl font-bold text-gray-900">{{ product.price }} kr</span>
            <div class="flex items-center gap-2">
              <span v-if="product.stock <= 5 && product.stock > 0" class="text-xs text-amber-600">
                {{ product.stock }} igjen
              </span>
              <span v-else-if="product.stock === 0" class="text-xs text-red-600">Utsolgt</span>
              <button
                :disabled="product.stock === 0"
                :class="[
                  'flex items-center gap-1.5 rounded-md px-3 py-2 text-sm font-medium shadow-sm transition',
                  addedIds.has(product.id)
                    ? 'bg-green-600 text-white'
                    : product.stock === 0
                      ? 'cursor-not-allowed bg-gray-100 text-gray-400'
                      : 'bg-blue-600 text-white hover:bg-blue-700',
                ]"
                @click="addToCart(product)"
              >
                <Check v-if="addedIds.has(product.id)" class="h-4 w-4" />
                <Plus v-else class="h-4 w-4" />
                {{ addedIds.has(product.id) ? 'Lagt til' : 'Legg i handlekurv' }}
              </button>
            </div>
          </div>
          <p v-if="cartQuantity(product.id) > 0" class="mt-2 text-xs text-blue-600">
            {{ cartQuantity(product.id) }} i handlekurven
          </p>
        </div>
      </div>
    </div>
  </div>
</template>
