<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Plus, Trash2, DollarSign } from 'lucide-vue-next'
import { useMySlipShares, useMyRebates, useCreateSlipShare, useDeleteSlipShare } from '@/composables/useSlipShares'

const { t } = useI18n()

const { data: shares, isLoading: sharesLoading } = useMySlipShares()
const { data: rebates, isLoading: rebatesLoading } = useMyRebates()
const { mutateAsync: createShare, isPending: creating } = useCreateSlipShare()
const { mutateAsync: deleteShare } = useDeleteSlipShare()

const showForm = ref(false)
const fromDate = ref('')
const toDate = ref('')
const notes = ref('')
const errorMsg = ref('')

const activeShares = computed(() => (shares.value ?? []).filter((s) => s.status === 'active'))
const totalRebate = computed(() =>
  (rebates.value ?? []).reduce((sum, r) => sum + r.rebate_amount, 0),
)

async function submit() {
  errorMsg.value = ''
  try {
    await createShare({ available_from: fromDate.value, available_to: toDate.value, notes: notes.value })
    showForm.value = false
    fromDate.value = ''
    toDate.value = ''
    notes.value = ''
  } catch (err: any) {
    errorMsg.value = err.message
  }
}

async function remove(id: string) {
  if (!confirm(t('booking.cancelConfirm'))) return
  try {
    await deleteShare(id)
  } catch (err: any) {
    errorMsg.value = err.message
  }
}
</script>

<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between">
      <div>
        <h2 class="text-xl font-semibold text-gray-900">{{ t('booking.slipSharing') }}</h2>
        <p class="mt-1 text-sm text-gray-600">{{ t('booking.slipSharingDesc', { pct: 25 }) }}</p>
      </div>
      <button
        type="button"
        class="inline-flex items-center gap-2 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700"
        @click="showForm = !showForm"
      >
        <Plus class="h-4 w-4" />
        {{ t('booking.addAvailability') }}
      </button>
    </div>

    <!-- Add form -->
    <form v-if="showForm" class="rounded-lg border border-gray-200 bg-white p-4 space-y-4" @submit.prevent="submit">
      <div class="grid grid-cols-2 gap-4">
        <div>
          <label class="block text-sm font-medium text-gray-700">{{ t('booking.dates') }} (fra)</label>
          <input v-model="fromDate" type="date" required class="mt-1 block w-full rounded-md border-gray-300 text-sm" />
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700">{{ t('booking.dates') }} (til)</label>
          <input v-model="toDate" type="date" required class="mt-1 block w-full rounded-md border-gray-300 text-sm" />
        </div>
      </div>
      <div>
        <label class="block text-sm font-medium text-gray-700">{{ t('booking.notes') }}</label>
        <input v-model="notes" type="text" class="mt-1 block w-full rounded-md border-gray-300 text-sm" :placeholder="'F.eks. ferie'" />
      </div>
      <div v-if="errorMsg" class="text-sm text-red-600">{{ errorMsg }}</div>
      <button
        type="submit"
        :disabled="creating"
        class="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
      >
        {{ creating ? t('common.loading') : t('common.save') }}
      </button>
    </form>

    <!-- Active shares -->
    <div v-if="sharesLoading" class="animate-pulse space-y-3">
      <div v-for="i in 2" :key="i" class="h-16 rounded-lg bg-gray-100" />
    </div>
    <div v-else-if="activeShares.length === 0" class="rounded-lg border border-dashed border-gray-300 p-6 text-center text-sm text-gray-500">
      {{ t('booking.noShares') }}
    </div>
    <div v-else class="space-y-3">
      <div
        v-for="share in activeShares"
        :key="share.id"
        class="flex items-center justify-between rounded-lg border border-gray-200 bg-white p-4"
      >
        <div>
          <p class="text-sm font-medium text-gray-900">{{ share.available_from }} — {{ share.available_to }}</p>
          <p v-if="share.notes" class="mt-0.5 text-xs text-gray-500">{{ share.notes }}</p>
        </div>
        <button type="button" class="text-red-500 hover:text-red-700" @click="remove(share.id)">
          <Trash2 class="h-4 w-4" />
        </button>
      </div>
    </div>

    <!-- Rebates -->
    <div class="mt-8">
      <h3 class="flex items-center gap-2 text-lg font-semibold text-gray-900">
        <DollarSign class="h-5 w-5 text-green-600" />
        {{ t('booking.rebates') }}
      </h3>
      <div v-if="rebatesLoading" class="mt-3 animate-pulse h-12 rounded bg-gray-100" />
      <div v-else-if="(rebates ?? []).length === 0" class="mt-3 text-sm text-gray-500">{{ t('booking.noRebates') }}</div>
      <div v-else class="mt-3 space-y-2">
        <div
          v-for="rebate in rebates"
          :key="rebate.id"
          class="flex items-center justify-between rounded-lg border border-gray-200 bg-white px-4 py-3 text-sm"
        >
          <span class="text-gray-600">{{ rebate.nights_rented }} {{ rebate.nights_rented === 1 ? 'natt' : 'netter' }}</span>
          <span class="font-semibold text-green-700">+{{ rebate.rebate_amount.toLocaleString('nb-NO') }} kr</span>
        </div>
        <div class="flex justify-between border-t pt-2 text-sm font-semibold">
          <span>{{ t('booking.rebateTotal') }}</span>
          <span class="text-green-700">{{ totalRebate.toLocaleString('nb-NO') }} kr</span>
        </div>
      </div>
    </div>
  </div>
</template>
