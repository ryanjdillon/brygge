<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'

const { t } = useI18n()
const client = useApiClient()
const queryClient = useQueryClient()

const tab = ref<'shares' | 'rebates'>('shares')
const statusFilter = ref('active')

const { data: shares, isLoading: sharesLoading } = useQuery({
  queryKey: ['admin-slip-shares', statusFilter],
  queryFn: async () =>
    unwrap(await client.GET('/api/v1/admin/slip-shares', {
      params: { query: { status: statusFilter.value } },
    })) ?? [],
})

const { data: rebates, isLoading: rebatesLoading } = useQuery({
  queryKey: ['admin-rebates'],
  queryFn: async () => unwrap(await client.GET('/api/v1/admin/slip-shares/rebates')),
})

const { mutateAsync: updateRebateStatus } = useMutation({
  mutationFn: async ({ id, status }: { id: string; status: string }) =>
    unwrap(await client.PUT('/api/v1/admin/slip-shares/rebates/{rebateID}', {
      params: { path: { rebateID: id } },
      body: { status } as any,
    })),
  onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admin-rebates'] }),
})

async function markCredited(id: string) {
  await updateRebateStatus({ id, status: 'credited' })
}
</script>

<template>
  <div class="space-y-6">
    <h1 class="text-2xl font-bold text-gray-900">{{ t('booking.slipSharing') }}</h1>

    <div class="flex gap-2">
      <button
        type="button"
        class="rounded-md px-3 py-1.5 text-sm font-medium"
        :class="tab === 'shares' ? 'bg-blue-600 text-white' : 'bg-gray-100 text-gray-700'"
        @click="tab = 'shares'"
      >
        {{ t('admin.slipShares.shares') }}
      </button>
      <button
        type="button"
        class="rounded-md px-3 py-1.5 text-sm font-medium"
        :class="tab === 'rebates' ? 'bg-blue-600 text-white' : 'bg-gray-100 text-gray-700'"
        @click="tab = 'rebates'"
      >
        {{ t('booking.rebates') }}
      </button>
    </div>

    <div v-if="tab === 'shares'">
      <div v-if="sharesLoading" class="animate-pulse space-y-3">
        <div v-for="i in 3" :key="i" class="h-16 rounded bg-gray-100" />
      </div>
      <table v-else class="w-full text-sm">
        <thead>
          <tr class="border-b text-left text-gray-500">
            <th class="pb-2 font-medium">{{ t('admin.slipShares.member') }}</th>
            <th class="pb-2 font-medium">{{ t('admin.slipShares.slip') }}</th>
            <th class="pb-2 font-medium">{{ t('admin.slipShares.from') }}</th>
            <th class="pb-2 font-medium">{{ t('admin.slipShares.to') }}</th>
            <th class="pb-2 font-medium">{{ t('common.status') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="s in shares" :key="s.id" class="border-b">
            <td class="py-2">{{ s.member_name }}</td>
            <td class="py-2 font-mono">{{ s.slip_number }}</td>
            <td class="py-2">{{ s.available_from }}</td>
            <td class="py-2">{{ s.available_to }}</td>
            <td class="py-2">
              <span class="rounded-full px-2 py-0.5 text-xs" :class="s.status === 'active' ? 'bg-green-100 text-green-700' : 'bg-gray-100 text-gray-600'">
                {{ s.status }}
              </span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-else>
      <div v-if="rebatesLoading" class="animate-pulse space-y-3">
        <div v-for="i in 3" :key="i" class="h-16 rounded bg-gray-100" />
      </div>
      <table v-else class="w-full text-sm">
        <thead>
          <tr class="border-b text-left text-gray-500">
            <th class="pb-2 font-medium">{{ t('admin.slipShares.nights') }}</th>
            <th class="pb-2 font-medium">{{ t('admin.slipShares.income') }}</th>
            <th class="pb-2 font-medium">{{ t('admin.slipShares.rebatePct') }}</th>
            <th class="pb-2 font-medium">{{ t('admin.slipShares.amount') }}</th>
            <th class="pb-2 font-medium">{{ t('common.status') }}</th>
            <th class="pb-2 font-medium"></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="r in rebates" :key="r.id" class="border-b">
            <td class="py-2">{{ r.nights_rented }}</td>
            <td class="py-2">{{ r.rental_income.toLocaleString('nb-NO') }} kr</td>
            <td class="py-2">{{ r.rebate_pct }}%</td>
            <td class="py-2 font-semibold text-green-700">{{ r.rebate_amount.toLocaleString('nb-NO') }} kr</td>
            <td class="py-2">
              <span class="rounded-full px-2 py-0.5 text-xs" :class="r.status === 'credited' ? 'bg-green-100 text-green-700' : 'bg-yellow-100 text-yellow-700'">
                {{ r.status }}
              </span>
            </td>
            <td class="py-2">
              <button
                v-if="r.status === 'pending'"
                type="button"
                class="text-xs text-blue-600 hover:underline"
                @click="markCredited(r.id)"
              >
                {{ t('admin.slipShares.markCredited') }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
