<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ArrowLeft, FileDown, Send, Trash2, RefreshCw } from 'lucide-vue-next'
import { useTotpGateStore } from '@/stores/totpGate'
import { useAuthStore } from '@/stores/auth'

interface Draft {
  id: string
  invoice_number: number
  user_id: string
  member_name: string
  member_email: string
  total_amount: number
  issue_date: string
  due_date: string
  price_item_name: string
  fiscal_year: number | null
  description: string
  created_at: string
}

const { t } = useI18n()
const auth = useAuthStore()
const totpGate = useTotpGateStore()

const drafts = ref<Draft[]>([])
const loading = ref(true)
const error = ref<string | null>(null)
const busyIds = ref<Set<string>>(new Set())
const selected = ref<Set<string>>(new Set())
const filterMember = ref('')
const filterPriceItem = ref('')
const filterYear = ref<number | ''>('')

async function ensureFreshTotp(): Promise<boolean> {
  if (auth.hasFreshTotp) return true
  return totpGate.open()
}

async function load() {
  loading.value = true
  error.value = null
  try {
    const res = await fetch('/api/v1/admin/financials/invoices/drafts', { credentials: 'include' })
    if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
    const body = await res.json()
    drafts.value = body.items ?? []
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    loading.value = false
  }
}

onMounted(load)

const filtered = computed(() => {
  return drafts.value.filter((d) => {
    if (filterMember.value && !d.member_name.toLowerCase().includes(filterMember.value.toLowerCase())
        && !d.member_email.toLowerCase().includes(filterMember.value.toLowerCase())) return false
    if (filterPriceItem.value && d.price_item_name !== filterPriceItem.value) return false
    if (filterYear.value !== '' && d.fiscal_year !== filterYear.value) return false
    return true
  })
})

const priceItemOptions = computed(() => {
  const set = new Set<string>()
  for (const d of drafts.value) if (d.price_item_name) set.add(d.price_item_name)
  return [...set].sort()
})
const yearOptions = computed(() => {
  const set = new Set<number>()
  for (const d of drafts.value) if (d.fiscal_year) set.add(d.fiscal_year)
  return [...set].sort((a, b) => b - a)
})

const totalAmount = computed(() => filtered.value.reduce((s, d) => s + Number(d.total_amount), 0))
const allFilteredSelected = computed(() =>
  filtered.value.length > 0 && filtered.value.every((d) => selected.value.has(d.id)),
)

function toggle(id: string) {
  const next = new Set(selected.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  selected.value = next
}
function toggleAll() {
  const next = new Set(selected.value)
  if (allFilteredSelected.value) for (const d of filtered.value) next.delete(d.id)
  else for (const d of filtered.value) next.add(d.id)
  selected.value = next
}

async function sendOne(id: string) {
  if (!(await ensureFreshTotp())) return
  busyIds.value.add(id)
  try {
    const res = await fetch(`/api/v1/admin/financials/invoices/${id}/send`, {
      method: 'POST', credentials: 'include',
    })
    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      error.value = `${res.status} ${txt}`
      return
    }
    drafts.value = drafts.value.filter((d) => d.id !== id)
    selected.value.delete(id)
  } finally {
    busyIds.value.delete(id)
  }
}

async function sendSelected() {
  if (selected.value.size === 0) return
  if (!confirm(t('admin.invoiceDrafts.sendConfirm', { n: selected.value.size }))) return
  if (!(await ensureFreshTotp())) return
  for (const id of [...selected.value]) {
    await sendOne(id)
  }
}

async function deleteOne(id: string) {
  if (!confirm(t('admin.invoiceDrafts.deleteConfirm'))) return
  if (!(await ensureFreshTotp())) return
  busyIds.value.add(id)
  try {
    const res = await fetch(`/api/v1/admin/financials/invoices/${id}`, {
      method: 'DELETE', credentials: 'include',
    })
    if (!res.ok && res.status !== 204) {
      const txt = await res.text().catch(() => '')
      error.value = `${res.status} ${txt}`
      return
    }
    drafts.value = drafts.value.filter((d) => d.id !== id)
    selected.value.delete(id)
  } finally {
    busyIds.value.delete(id)
  }
}

async function deleteSelected() {
  if (selected.value.size === 0) return
  if (!confirm(t('admin.invoiceDrafts.deleteSelectedConfirm', { n: selected.value.size }))) return
  if (!(await ensureFreshTotp())) return
  for (const id of [...selected.value]) {
    await deleteOne(id)
  }
}

function formatNOK(n: number): string {
  return new Intl.NumberFormat('nb-NO', { style: 'currency', currency: 'NOK' }).format(n)
}
</script>

<template>
  <div>
    <div class="mb-3 flex items-center gap-2">
      <RouterLink to="/admin/accounting" class="text-sm text-gray-600 hover:text-gray-900">
        <ArrowLeft class="inline h-4 w-4" /> {{ t('admin.accounting.title') }}
      </RouterLink>
    </div>

    <div class="flex items-center justify-between">
      <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.invoiceDrafts.title') }}</h1>
      <button
        class="inline-flex items-center gap-1 rounded-md border border-gray-300 px-3 py-1.5 text-sm hover:bg-gray-50"
        @click="load"
      >
        <RefreshCw class="h-4 w-4" /> {{ t('common.refresh') }}
      </button>
    </div>

    <p class="mt-1 text-sm text-gray-600">{{ t('admin.invoiceDrafts.subtitle') }}</p>

    <p v-if="loading" class="mt-4 text-sm text-gray-500">{{ t('common.loading') }}…</p>
    <p v-else-if="error" class="mt-4 rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">{{ error }}</p>

    <template v-else-if="drafts.length">
      <!-- Filters -->
      <div class="mt-4 flex flex-wrap items-center gap-2">
        <input
          v-model="filterMember"
          type="search"
          :placeholder="t('admin.invoiceDrafts.filterMember')"
          class="rounded-md border border-gray-300 px-2 py-1 text-sm"
        />
        <select v-model="filterPriceItem" class="rounded-md border border-gray-300 px-2 py-1 text-sm">
          <option value="">{{ t('admin.invoiceDrafts.filterAllItems') }}</option>
          <option v-for="n in priceItemOptions" :key="n" :value="n">{{ n }}</option>
        </select>
        <select v-model.number="filterYear" class="rounded-md border border-gray-300 px-2 py-1 text-sm">
          <option value="">{{ t('admin.invoiceDrafts.filterAllYears') }}</option>
          <option v-for="y in yearOptions" :key="y" :value="y">{{ y }}</option>
        </select>
        <span class="ml-auto text-xs text-gray-500">
          {{ t('admin.invoiceDrafts.summary', { n: filtered.length, total: formatNOK(totalAmount) }) }}
        </span>
      </div>

      <!-- Bulk action bar -->
      <div
        v-if="selected.size > 0"
        class="mt-3 flex flex-wrap items-center gap-3 rounded-md border border-blue-200 bg-blue-50 px-3 py-2 text-sm"
      >
        <span class="font-medium text-blue-900">
          {{ t('admin.invoiceDrafts.selectedCount', { n: selected.size }) }}
        </span>
        <button
          class="ml-auto inline-flex items-center gap-1 rounded-md bg-blue-600 px-3 py-1 text-xs font-semibold text-white hover:bg-blue-700"
          @click="sendSelected"
        >
          <Send class="h-3.5 w-3.5" /> {{ t('admin.invoiceDrafts.sendSelected') }}
        </button>
        <button
          class="inline-flex items-center gap-1 rounded-md border border-red-300 bg-white px-3 py-1 text-xs font-semibold text-red-700 hover:bg-red-50"
          @click="deleteSelected"
        >
          <Trash2 class="h-3.5 w-3.5" /> {{ t('admin.invoiceDrafts.deleteSelected') }}
        </button>
      </div>

      <!-- Table -->
      <div class="mt-3 overflow-x-auto">
        <table class="min-w-full divide-y divide-gray-200">
          <thead class="bg-gray-50">
            <tr>
              <th class="w-8 px-2 py-2 text-center">
                <input
                  type="checkbox"
                  :checked="allFilteredSelected"
                  class="rounded border-gray-300"
                  @change="toggleAll"
                />
              </th>
              <th class="px-3 py-2 text-left text-xs font-medium uppercase tracking-wider text-gray-500">#</th>
              <th class="px-3 py-2 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.invoiceDrafts.member') }}</th>
              <th class="px-3 py-2 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.invoiceDrafts.priceItem') }}</th>
              <th class="px-3 py-2 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.invoiceDrafts.year') }}</th>
              <th class="px-3 py-2 text-right text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.invoiceDrafts.amount') }}</th>
              <th class="px-3 py-2 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.invoiceDrafts.dueDate') }}</th>
              <th class="px-3 py-2 text-right text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('common.actions') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-200 bg-white">
            <tr
              v-for="d in filtered"
              :key="d.id"
              :class="{ 'bg-blue-50/50': selected.has(d.id) }"
            >
              <td class="px-2 py-2 text-center">
                <input
                  type="checkbox"
                  :checked="selected.has(d.id)"
                  class="rounded border-gray-300"
                  @change="toggle(d.id)"
                />
              </td>
              <td class="whitespace-nowrap px-3 py-2 font-mono text-xs text-gray-500">{{ d.invoice_number }}</td>
              <td class="px-3 py-2 text-sm">
                <div class="font-medium text-gray-900">{{ d.member_name }}</div>
                <div class="text-xs text-gray-500">{{ d.member_email }}</div>
              </td>
              <td class="px-3 py-2 text-sm text-gray-700">{{ d.price_item_name || d.description || '—' }}</td>
              <td class="whitespace-nowrap px-3 py-2 text-sm text-gray-600">{{ d.fiscal_year ?? '—' }}</td>
              <td class="whitespace-nowrap px-3 py-2 text-right text-sm tabular-nums">{{ formatNOK(Number(d.total_amount)) }}</td>
              <td class="whitespace-nowrap px-3 py-2 text-sm text-gray-600">{{ d.due_date }}</td>
              <td class="whitespace-nowrap px-3 py-2 text-right text-sm">
                <div class="flex justify-end gap-2">
                  <a
                    :href="`/api/v1/admin/financials/invoices/${d.id}/pdf`"
                    target="_blank"
                    rel="noopener"
                    class="text-gray-500 hover:text-gray-800"
                    :title="t('admin.invoiceDrafts.previewPdf')"
                  >
                    <FileDown class="h-4 w-4" />
                  </a>
                  <button
                    class="text-blue-600 hover:text-blue-800 disabled:opacity-50"
                    :disabled="busyIds.has(d.id)"
                    :title="t('admin.invoiceDrafts.send')"
                    @click="sendOne(d.id)"
                  >
                    <Send class="h-4 w-4" />
                  </button>
                  <button
                    class="text-red-600 hover:text-red-800 disabled:opacity-50"
                    :disabled="busyIds.has(d.id)"
                    :title="t('admin.invoiceDrafts.delete')"
                    @click="deleteOne(d.id)"
                  >
                    <Trash2 class="h-4 w-4" />
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>

    <p v-else class="mt-6 text-sm text-gray-500">{{ t('admin.invoiceDrafts.empty') }}</p>
  </div>
</template>
