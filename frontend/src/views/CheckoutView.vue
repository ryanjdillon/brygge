<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useCartStore } from '@/stores/cart'
import { useApiClient, unwrap } from '@/lib/apiClient'
import { Trash2, Minus, Plus, ShoppingCart } from 'lucide-vue-next'

const { t } = useI18n()
const router = useRouter()
const cart = useCartStore()
const client = useApiClient()

const isSubmitting = ref(false)
const error = ref('')

async function checkout() {
  if (cart.items.length === 0) return

  isSubmitting.value = true
  error.value = ''

  try {
    const lines = cart.items.map((item) => ({
      product_id: item.type === 'product' ? item.id : undefined,
      price_item_id: item.type === 'price_item' ? item.id : undefined,
      variant_id: item.variantId || undefined,
      name: item.name,
      quantity: item.quantity,
      unit_price: item.unitPrice,
    }))

    const result = unwrap(await client.POST('/api/v1/orders', {
      body: { lines } as any,
    })) as { id: string; checkout_url: string; total_amount: number }

    cart.clear()
    router.push(result.checkout_url)
  } catch (e: any) {
    error.value = e?.message ?? t('merchandise.somethingWentWrong')
  } finally {
    isSubmitting.value = false
  }
}
</script>

<template>
  <div class="mx-auto max-w-3xl px-4 py-12 sm:px-6 lg:px-8">
    <h1 class="text-3xl font-bold text-gray-900">{{ t('merchandise.cart') }}</h1>

    <div v-if="cart.items.length === 0" class="mt-10 text-center">
      <ShoppingCart class="mx-auto h-16 w-16 text-gray-300" />
      <p class="mt-4 text-gray-500">{{ t('merchandise.cartEmpty') }}</p>
      <router-link
        to="/merchandise"
        class="mt-4 inline-block rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white hover:bg-blue-700"
      >
        {{ t('merchandise.backToShop') }}
      </router-link>
    </div>

    <div v-else class="mt-8 space-y-4">
      <div
        v-for="item in cart.items"
        :key="item.id"
        class="flex items-center justify-between rounded-lg border border-gray-200 bg-white p-4"
      >
        <div class="flex-1">
          <h3 class="font-medium text-gray-900">{{ item.name }}</h3>
          <p v-if="item.size || item.color" class="text-xs text-gray-400">
            <span v-if="item.size">{{ item.size }}</span>
            <span v-if="item.size && item.color"> / </span>
            <span v-if="item.color">{{ item.color }}</span>
          </p>
          <p class="text-sm text-gray-500">{{ item.unitPrice }} kr {{ t('merchandise.each') }}</p>
        </div>

        <div class="flex items-center gap-3">
          <div class="flex items-center gap-1 rounded-md border border-gray-200">
            <button
              class="px-2 py-1 text-gray-500 hover:text-gray-700"
              @click="cart.updateQuantity(item.id, item.quantity - 1)"
            >
              <Minus class="h-4 w-4" />
            </button>
            <span class="w-8 text-center text-sm font-medium">{{ item.quantity }}</span>
            <button
              class="px-2 py-1 text-gray-500 hover:text-gray-700"
              @click="cart.updateQuantity(item.id, item.quantity + 1)"
            >
              <Plus class="h-4 w-4" />
            </button>
          </div>

          <span class="w-24 text-right font-semibold text-gray-900">
            {{ (item.unitPrice * item.quantity).toLocaleString('nb-NO') }} kr
          </span>

          <button class="text-gray-400 hover:text-red-600" @click="cart.removeItem(item.id)">
            <Trash2 class="h-4 w-4" />
          </button>
        </div>
      </div>

      <div class="rounded-lg border border-gray-200 bg-gray-50 p-4">
        <div class="flex items-center justify-between text-lg font-bold text-gray-900">
          <span>{{ t('merchandise.total') }}</span>
          <span>{{ cart.totalAmount.toLocaleString('nb-NO') }} kr</span>
        </div>
      </div>

      <div v-if="error" class="rounded-md bg-red-50 p-3 text-sm text-red-800">
        {{ error }}
      </div>

      <div class="flex justify-end gap-3">
        <router-link
          to="/merchandise"
          class="rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-semibold text-gray-700 hover:bg-gray-50"
        >
          {{ t('merchandise.continueShopping') }}
        </router-link>
        <button
          :disabled="isSubmitting"
          class="flex items-center gap-2 rounded-md bg-orange-500 px-6 py-2 text-sm font-semibold text-white shadow-sm hover:bg-orange-600 disabled:opacity-50"
          @click="checkout"
        >
          <img
            src="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24' fill='white'%3E%3Cpath d='M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2z'/%3E%3C/svg%3E"
            alt=""
            class="h-5 w-5"
          />
          {{ isSubmitting ? t('merchandise.processingPayment') : t('merchandise.payWithVipps') }}
        </button>
      </div>
    </div>
  </div>
</template>
