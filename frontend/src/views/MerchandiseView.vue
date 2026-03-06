<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { ShoppingBag } from 'lucide-vue-next'

const { t } = useI18n()

const products = [
  { key: 'burgee', price: '350 kr' },
  { key: 'tshirt', price: '299 kr' },
  { key: 'cap', price: '199 kr' },
]
</script>

<template>
  <div class="mx-auto max-w-7xl px-4 py-12 sm:px-6 lg:px-8">
    <h1 class="text-3xl font-bold text-gray-900">{{ t('merchandise.title') }}</h1>

    <div class="mt-4 flex items-center gap-2 rounded-lg border border-amber-200 bg-amber-50 p-4 text-amber-800">
      <ShoppingBag class="h-5 w-5 shrink-0" />
      <p>{{ t('merchandise.comingSoon') }}</p>
    </div>

    <div class="mt-10 grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
      <div
        v-for="product in products"
        :key="product.key"
        class="rounded-lg border border-gray-200 p-6 opacity-60"
      >
        <div class="flex h-40 items-center justify-center rounded-md bg-gray-100 text-gray-400">
          <ShoppingBag class="h-12 w-12" />
        </div>
        <h3 class="mt-4 text-lg font-semibold text-gray-900">
          {{ t(`merchandise.${product.key}`) }}
        </h3>
        <p class="mt-1 text-gray-600">{{ product.price }}</p>
      </div>
    </div>
  </div>
</template>
