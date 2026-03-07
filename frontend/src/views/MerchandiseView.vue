<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'
import { useCartStore } from '@/stores/cart'
import { ShoppingCart, X, Check, Plus } from 'lucide-vue-next'

const { t } = useI18n()
const { fetchApi } = useApi()
const cart = useCartStore()

interface Variant {
  id: string
  size: string
  color: string
  stock: number
  price_override: number | null
  image_url: string
  sort_order: number
}

interface Product {
  id: string
  name: string
  description: string
  price: number
  currency: string
  image_url: string
  stock: number
  variants: Variant[]
}

const { data: response, isLoading } = useQuery({
  queryKey: ['products'],
  queryFn: () => fetchApi<{ products: Product[] }>('/api/v1/products'),
})

const selectedProduct = ref<Product | null>(null)
const selectedSize = ref('')
const selectedColor = ref('')
const justAdded = ref(false)

function openProduct(product: Product) {
  selectedProduct.value = product
  selectedSize.value = ''
  selectedColor.value = ''
  justAdded.value = false

  // Auto-select if only one option
  const sizes = availableSizes.value
  const colors = availableColors.value
  if (sizes.length === 1) selectedSize.value = sizes[0]
  if (colors.length === 1) selectedColor.value = colors[0]
}

function closeModal() {
  selectedProduct.value = null
}

const hasVariants = computed(() => (selectedProduct.value?.variants.length ?? 0) > 0)

const availableSizes = computed(() => {
  if (!selectedProduct.value) return []
  const sizes = [...new Set(selectedProduct.value.variants.map((v) => v.size).filter(Boolean))]
  return sizes
})

const availableColors = computed(() => {
  if (!selectedProduct.value) return []
  const colors = [...new Set(selectedProduct.value.variants.map((v) => v.color).filter(Boolean))]
  return colors
})

function variantForSelection(): Variant | undefined {
  if (!selectedProduct.value) return undefined
  return selectedProduct.value.variants.find(
    (v) =>
      (v.size === selectedSize.value || (!v.size && !selectedSize.value)) &&
      (v.color === selectedColor.value || (!v.color && !selectedColor.value)),
  )
}

function isSizeAvailable(size: string): boolean {
  if (!selectedProduct.value) return false
  return selectedProduct.value.variants.some(
    (v) =>
      v.size === size &&
      v.stock > 0 &&
      (!selectedColor.value || v.color === selectedColor.value || !v.color),
  )
}

function isColorAvailable(color: string): boolean {
  if (!selectedProduct.value) return false
  return selectedProduct.value.variants.some(
    (v) =>
      v.color === color &&
      v.stock > 0 &&
      (!selectedSize.value || v.size === selectedSize.value || !v.size),
  )
}

const canAddToCart = computed(() => {
  if (!selectedProduct.value) return false
  if (!hasVariants.value) return selectedProduct.value.stock > 0
  const variant = variantForSelection()
  return !!variant && variant.stock > 0
})

const effectivePrice = computed(() => {
  if (!selectedProduct.value) return 0
  if (!hasVariants.value) return selectedProduct.value.price
  const variant = variantForSelection()
  return variant?.price_override ?? selectedProduct.value.price
})

const modalImage = computed(() => {
  if (!selectedProduct.value) return ''
  if (selectedColor.value) {
    const variantWithImage = selectedProduct.value.variants.find(
      (v) => v.color === selectedColor.value && v.image_url,
    )
    if (variantWithImage) return variantWithImage.image_url
  }
  return selectedProduct.value.image_url
})

const totalStock = computed(() => {
  if (!selectedProduct.value) return 0
  if (!hasVariants.value) return selectedProduct.value.stock
  return selectedProduct.value.variants.reduce((sum, v) => sum + v.stock, 0)
})

function isInStock(product: Product): boolean {
  if (product.variants.length > 0) {
    return product.variants.some((v) => v.stock > 0)
  }
  return product.stock > 0
}

function priceRange(product: Product): string {
  if (product.variants.length === 0) return `${product.price} kr`
  const prices = product.variants
    .map((v) => v.price_override ?? product.price)
    .filter((p, i, arr) => arr.indexOf(p) === i)
    .sort((a, b) => a - b)
  if (prices.length === 1) return `${prices[0]} kr`
  return `${prices[0]}–${prices[prices.length - 1]} kr`
}

function addToCart() {
  if (!selectedProduct.value || !canAddToCart.value) return

  const product = selectedProduct.value
  const variant = variantForSelection()

  const nameParts = [product.name]
  if (selectedSize.value) nameParts.push(selectedSize.value)
  if (selectedColor.value) nameParts.push(selectedColor.value)

  cart.addItem({
    id: product.id,
    type: 'product',
    name: nameParts.join(' – '),
    unitPrice: effectivePrice.value,
    imageUrl: product.image_url,
    variantId: variant?.id,
    size: selectedSize.value || undefined,
    color: selectedColor.value || undefined,
  })

  justAdded.value = true
  setTimeout(() => {
    justAdded.value = false
  }, 1500)
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
        <ShoppingCart class="h-4 w-4" aria-hidden="true" />
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
      <button
        v-for="product in response.products"
        :key="product.id"
        class="flex flex-col rounded-lg border border-gray-200 bg-white text-left transition hover:border-gray-300 hover:shadow-md"
        @click="openProduct(product)"
      >
        <div class="flex h-48 items-center justify-center overflow-hidden rounded-t-lg bg-gray-50 text-gray-300">
          <img
            v-if="product.image_url"
            :src="product.image_url"
            :alt="product.name"
            class="h-full w-full object-cover"
          />
          <ShoppingCart v-else class="h-16 w-16" aria-hidden="true" />
        </div>
        <div class="flex flex-1 flex-col p-5">
          <h3 class="text-lg font-semibold text-gray-900">{{ product.name }}</h3>
          <p class="mt-1 flex-1 text-sm text-gray-500">{{ product.description }}</p>
          <div class="mt-4 flex items-center justify-between">
            <span class="text-xl font-bold text-gray-900">{{ priceRange(product) }}</span>
            <span v-if="!isInStock(product)" class="text-xs font-medium text-red-600">Utsolgt</span>
          </div>
        </div>
      </button>
    </div>

    <!-- Product detail modal -->
    <div
      v-if="selectedProduct"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      role="dialog"
      aria-modal="true"
      @click.self="closeModal"
    >
      <div class="w-full max-w-lg rounded-xl bg-white shadow-xl">
        <div class="flex items-start justify-between border-b border-gray-100 p-6 pb-4">
          <div>
            <h2 class="text-xl font-bold text-gray-900">{{ selectedProduct.name }}</h2>
            <p class="mt-1 text-sm text-gray-500">{{ selectedProduct.description }}</p>
          </div>
          <button
            class="rounded-lg p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-600"
            :aria-label="t('common.close')"
            @click="closeModal"
          >
            <X class="h-5 w-5" />
          </button>
        </div>

        <div class="flex items-center justify-center overflow-hidden bg-gray-50" :class="modalImage ? 'h-64' : 'h-32'">
          <img
            v-if="modalImage"
            :src="modalImage"
            :alt="selectedProduct.name"
            class="h-full w-full object-contain"
          />
          <ShoppingCart v-else class="h-16 w-16 text-gray-300" aria-hidden="true" />
        </div>

        <div class="space-y-5 p-6">
          <div class="text-2xl font-bold text-gray-900">
            {{ effectivePrice }} kr
          </div>

          <!-- Size selector -->
          <div v-if="availableSizes.length > 0">
            <label class="block text-sm font-medium text-gray-700">Størrelse</label>
            <div class="mt-2 flex flex-wrap gap-2">
              <button
                v-for="size in availableSizes"
                :key="size"
                :class="[
                  'rounded-lg border px-4 py-2 text-sm font-medium transition',
                  selectedSize === size
                    ? 'border-blue-600 bg-blue-50 text-blue-700'
                    : isSizeAvailable(size)
                      ? 'border-gray-200 text-gray-700 hover:border-gray-300'
                      : 'cursor-not-allowed border-gray-100 text-gray-300 line-through',
                ]"
                :disabled="!isSizeAvailable(size)"
                @click="selectedSize = size"
              >
                {{ size }}
              </button>
            </div>
          </div>

          <!-- Color selector -->
          <div v-if="availableColors.length > 0">
            <label class="block text-sm font-medium text-gray-700">Farge</label>
            <div class="mt-2 flex flex-wrap gap-2">
              <button
                v-for="color in availableColors"
                :key="color"
                :class="[
                  'rounded-lg border px-4 py-2 text-sm font-medium transition',
                  selectedColor === color
                    ? 'border-blue-600 bg-blue-50 text-blue-700'
                    : isColorAvailable(color)
                      ? 'border-gray-200 text-gray-700 hover:border-gray-300'
                      : 'cursor-not-allowed border-gray-100 text-gray-300 line-through',
                ]"
                :disabled="!isColorAvailable(color)"
                @click="selectedColor = color"
              >
                {{ color }}
              </button>
            </div>
          </div>

          <!-- Stock info -->
          <div v-if="hasVariants && canAddToCart" class="text-xs text-gray-400">
            {{ variantForSelection()?.stock }} på lager
          </div>
          <div v-else-if="!hasVariants && totalStock > 0 && totalStock <= 5" class="text-xs text-amber-600">
            {{ totalStock }} igjen
          </div>

          <!-- Add to cart button -->
          <button
            :disabled="!canAddToCart"
            :class="[
              'flex w-full items-center justify-center gap-2 rounded-lg px-6 py-3 text-sm font-semibold shadow-sm transition',
              justAdded
                ? 'bg-green-600 text-white'
                : canAddToCart
                  ? 'bg-blue-600 text-white hover:bg-blue-700'
                  : 'cursor-not-allowed bg-gray-100 text-gray-400',
            ]"
            @click="addToCart"
          >
            <Check v-if="justAdded" class="h-5 w-5" aria-hidden="true" />
            <Plus v-else class="h-5 w-5" aria-hidden="true" />
            {{ justAdded ? 'Lagt til i handlekurven' : 'Legg i handlekurv' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
