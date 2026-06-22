<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'
import { useExportCSV } from '@/composables/useFinancials'
import { AlertTriangle, Download, Mail } from 'lucide-vue-next'
import { formatNOK } from '@/lib/format'
import SortableTh from '@/components/admin/SortableTh.vue'

const { t } = useI18n()
const client = useApiClient()

type SortField = 'member' | 'amount' | 'days_overdue'
const sortField = ref<SortField>('days_overdue')
const sortDir = ref<'asc' | 'desc'>('desc')

function setSort(field: SortField) {
  if (sortField.value === field) {
    sortDir.value = sortDir.value === 'asc' ? 'desc' : 'asc'
  } else {
    sortField.value = field
    sortDir.value = field === 'days_overdue' ? 'desc' : 'asc'
  }
}

const PAGE_SIZE = 100
const offset = ref(0)

const { data: response, isLoading, error } = useQuery({
  queryKey: ['financials', 'overdue', offset],
  queryFn: async () => {
    const res = unwrap(await client.GET('/api/v1/admin/financials/overdue', {
      params: { query: { limit: PAGE_SIZE, offset: offset.value } as any },
    }))
    return res as unknown as { items: any[]; has_more: boolean; limit: number; offset: number }
  },
  staleTime: 2 * 60 * 1000,
})

const items = computed(() => response.value?.items ?? [])
const hasMore = computed(() => response.value?.has_more ?? false)
const hasPrev = computed(() => offset.value > 0)

function nextPage() { if (hasMore.value) offset.value += PAGE_SIZE }
function prevPage() { if (hasPrev.value) offset.value = Math.max(0, offset.value - PAGE_SIZE) }

const sorted = computed(() => {
  const list = [...items.value]
  list.sort((a, b) => {
    let cmp = 0
    if (sortField.value === 'member') cmp = (a.user_name ?? '').localeCompare(b.user_name ?? '')
    else if (sortField.value === 'amount') cmp = (a.amount ?? 0) - (b.amount ?? 0)
    else if (sortField.value === 'days_overdue') cmp = (a.days_overdue ?? 0) - (b.days_overdue ?? 0)
    return sortDir.value === 'asc' ? cmp : -cmp
  })
  return list
})

const { downloadCSV } = useExportCSV()

function handleExportOverdue() {
  downloadCSV({ status: 'pending' })
}

function handleSendReminder(_paymentId: string) {
  // Stub for reminder functionality
}
</script>

<template>
  <div>
    <div class="flex items-center justify-between">
      <div class="flex items-center gap-3">
        <AlertTriangle class="h-6 w-6 text-red-600" />
        <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.financials.overduePayments') }}</h1>
      </div>
      <button
        class="inline-flex items-center gap-2 rounded-md bg-gray-100 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-200"
        @click="handleExportOverdue"
      >
        <Download class="h-4 w-4" />
        {{ t('admin.financials.exportCSV') }}
      </button>
    </div>

    <div v-if="isLoading" class="mt-8 text-center text-gray-500">
      {{ t('common.loading') }}...
    </div>

    <div v-else-if="error" class="mt-8 text-center text-red-600">
      {{ t('admin.financials.loadError') }}
    </div>

    <div v-else-if="!sorted.length" class="mt-8 text-center text-gray-500">
      {{ t('admin.financials.noOverdue') }}
    </div>

    <div v-else class="mt-6 overflow-x-auto">
      <table class="min-w-full divide-y divide-gray-200">
        <thead class="bg-gray-50">
          <tr>
            <th scope="col" class="w-10 px-3 py-3 text-right text-xs font-medium uppercase tracking-wider text-gray-400">#</th>
            <SortableTh :active="sortField === 'member'" :dir="sortDir" @click="setSort('member')">
              {{ t('admin.financials.member') }}
            </SortableTh>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('contact.email') }}
            </th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('contact.phone') }}
            </th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('admin.financials.paymentType') }}
            </th>
            <SortableTh :active="sortField === 'amount'" :dir="sortDir" class="text-right" @click="setSort('amount')">
              {{ t('admin.financials.amount') }}
            </SortableTh>
            <SortableTh :active="sortField === 'days_overdue'" :dir="sortDir" class="text-right" @click="setSort('days_overdue')">
              {{ t('admin.financials.daysOverdue') }}
            </SortableTh>
            <th scope="col" class="px-4 py-3 text-right text-xs font-medium uppercase tracking-wider text-gray-500">
              {{ t('common.actions') }}
            </th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 bg-white">
          <tr v-for="(item, index) in sorted" :key="item.id">
            <td class="whitespace-nowrap px-3 py-3 text-right text-xs text-gray-400 tabular-nums">{{ offset + index + 1 }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-sm font-medium text-gray-900">
              {{ item.user_name }}
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-600">
              {{ item.user_email }}
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-600">
              {{ item.user_phone || '-' }}
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-600">
              {{ item.type }}
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-right text-sm font-medium text-gray-900">
              {{ formatNOK(item.amount) }}
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-right">
              <span class="inline-flex rounded-full bg-red-100 px-2 py-0.5 text-xs font-medium text-red-800">
                {{ t('admin.financials.daysOverdueCount', { count: item.days_overdue }) }}
              </span>
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-right">
              <button
                class="inline-flex items-center gap-1 text-sm text-brand-600 hover:text-brand-800"
                :title="t('admin.financials.sendReminder')"
                @click="handleSendReminder(item.id)"
              >
                <Mail class="h-4 w-4" />
                {{ t('admin.financials.sendReminder') }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>

      <div v-if="hasPrev || hasMore" class="mt-3 flex items-center justify-between text-sm text-gray-600">
        <span class="text-xs text-gray-400">{{ t('common.showingFrom', { from: offset + 1, to: offset + sorted.length }) }}</span>
        <div class="flex gap-2">
          <button class="rounded-md px-3 py-1 ring-1 ring-gray-300 hover:bg-gray-50 disabled:opacity-40" :disabled="!hasPrev" @click="prevPage">{{ t('common.previous') }}</button>
          <button class="rounded-md px-3 py-1 ring-1 ring-gray-300 hover:bg-gray-50 disabled:opacity-40" :disabled="!hasMore" @click="nextPage">{{ t('common.next') }}</button>
        </div>
      </div>
    </div>
  </div>
</template>
