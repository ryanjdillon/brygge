<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useApi } from '@/composables/useApi'
import { CheckCircle, XCircle, Loader2 } from 'lucide-vue-next'

const { t } = useI18n()
const route = useRoute()
const { fetchApi } = useApi()

const status = ref<'loading' | 'success' | 'error'>('loading')
const orderData = ref<any>(null)

onMounted(async () => {
  const orderId = route.query.order as string
  if (!orderId) {
    status.value = 'error'
    return
  }

  try {
    // Stub: auto-confirm the order (in production, Vipps callback does this)
    await fetchApi(`/api/v1/orders/${orderId}/confirm`, { method: 'POST' })
    const order = await fetchApi(`/api/v1/orders/${orderId}`)
    orderData.value = order
    status.value = 'success'
  } catch {
    status.value = 'error'
  }
})
</script>

<template>
  <div class="mx-auto max-w-lg px-4 py-16 text-center sm:px-6">
    <div v-if="status === 'loading'" class="space-y-4">
      <Loader2 class="mx-auto h-12 w-12 animate-spin text-blue-600" />
      <p class="text-gray-500">{{ t('merchandise.processingPayment') }}</p>
    </div>

    <div v-else-if="status === 'success'" class="space-y-4">
      <CheckCircle class="mx-auto h-16 w-16 text-green-600" />
      <h1 class="text-2xl font-bold text-gray-900">{{ t('merchandise.paymentReceived') }}</h1>
      <p class="text-gray-500">
        {{ t('merchandise.orderConfirmed', { id: orderData?.id?.slice(0, 8) }) }}
      </p>
      <div v-if="orderData?.lines?.length" class="mx-auto mt-6 max-w-sm rounded-lg border border-gray-200 bg-white p-4 text-left">
        <div v-for="line in orderData.lines" :key="line.id" class="flex justify-between border-b border-gray-100 py-2 last:border-0">
          <span class="text-sm text-gray-700">{{ line.name }} x{{ line.quantity }}</span>
          <span class="text-sm font-medium text-gray-900">{{ line.total_price }} kr</span>
        </div>
        <div class="mt-2 flex justify-between border-t border-gray-200 pt-2 font-bold text-gray-900">
          <span>{{ t('merchandise.total') }}</span>
          <span>{{ orderData.total_amount }} kr</span>
        </div>
      </div>
      <router-link
        to="/"
        class="mt-6 inline-block rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white hover:bg-blue-700"
      >
        {{ t('merchandise.backToHome') }}
      </router-link>
    </div>

    <div v-else class="space-y-4">
      <XCircle class="mx-auto h-16 w-16 text-red-500" />
      <h1 class="text-2xl font-bold text-gray-900">{{ t('merchandise.somethingWentWrong') }}</h1>
      <p class="text-gray-500">{{ t('merchandise.paymentError') }}</p>
      <router-link
        to="/merchandise"
        class="mt-4 inline-block rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white hover:bg-blue-700"
      >
        {{ t('merchandise.backToShop') }}
      </router-link>
    </div>
  </div>
</template>
