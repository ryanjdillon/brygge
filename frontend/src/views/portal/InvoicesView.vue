<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useMyInvoices } from '@/composables/useMyInvoices'
import { usePaymentDataUpdatedAt } from '@/composables/usePaymentDataUpdatedAt'
import InvoiceList from '@/components/portal/InvoiceList.vue'
import LastUpdated from '@/components/ui/LastUpdated.vue'

const { t } = useI18n()
const { unpaid, paid, invoices, isLoading } = useMyInvoices()
const { updatedAt: paymentsUpdatedAt } = usePaymentDataUpdatedAt()
</script>

<template>
  <div>
    <h1 class="text-2xl font-bold text-gray-900">{{ t('portal.invoices.title') }}</h1>
    <p class="mt-1 text-sm text-gray-500">{{ t('portal.invoices.subtitle') }}</p>
    <LastUpdated :at="paymentsUpdatedAt" class="mt-1" />

    <div v-if="isLoading" class="mt-6 text-gray-500">{{ t('common.loading') }}...</div>

    <template v-else-if="invoices.length">
      <section v-if="unpaid.length" class="mt-6 rounded-lg border border-gray-200 bg-white p-5 shadow-sm">
        <h2 class="text-sm font-semibold uppercase tracking-wider text-gray-400">
          {{ t('portal.invoices.unpaidHeading') }}
        </h2>
        <InvoiceList :invoices="unpaid" />
      </section>

      <section v-if="paid.length" class="mt-6 rounded-lg border border-gray-200 bg-white p-5 shadow-sm">
        <h2 class="text-sm font-semibold uppercase tracking-wider text-gray-400">
          {{ t('portal.invoices.paidHeading') }}
        </h2>
        <InvoiceList :invoices="paid" />
      </section>
    </template>

    <p v-else class="mt-6 rounded-lg border border-gray-200 bg-white p-8 text-center text-sm text-gray-500 shadow-sm">
      {{ t('portal.invoices.none') }}
    </p>
  </div>
</template>
