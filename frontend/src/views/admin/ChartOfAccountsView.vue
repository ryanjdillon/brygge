<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Plus, Check, X, ChevronUp, ChevronDown } from 'lucide-vue-next'
import {
  useAccountsList,
  useSeedAccounts,
  useCreateAccount,
  useUpdateAccount,
  type Account,
} from '@/composables/useAccounting'
import AccountCodeChip from '@/components/ui/AccountCodeChip.vue'

const { t } = useI18n()

const { data: accounts, isLoading, isError } = useAccountsList()
const seedMutation = useSeedAccounts()
const createMutation = useCreateAccount()
const updateMutation = useUpdateAccount()

const hasAccounts = computed(() => (accounts.value?.length ?? 0) > 0)

const typeLabels = computed<Record<string, string>>(() => ({
  asset: t('admin.accounting.accounts.typeAsset'),
  liability: t('admin.accounting.accounts.typeLiability'),
  revenue: t('admin.accounting.accounts.typeRevenue'),
  expense: t('admin.accounting.accounts.typeExpense'),
}))

const mvaLabels = computed<Record<string, string>>(() => ({
  eligible: t('admin.accounting.accounts.mvaEligible'),
  ineligible: t('admin.accounting.accounts.mvaIneligible'),
  partial: t('admin.accounting.accounts.mvaPartial'),
  not_applicable: t('admin.accounting.accounts.mvaNA'),
}))

const typeFilterOptions = ['all', 'asset', 'liability', 'revenue', 'expense']
const typeFilter = ref('all')
const searchQuery = ref('')

type SortKey = 'code' | 'name' | 'account_type' | 'mva_eligible'
const sortKey = ref<SortKey>('code')
const sortAsc = ref(true)

function toggleSort(key: SortKey) {
  if (sortKey.value === key) {
    sortAsc.value = !sortAsc.value
  } else {
    sortKey.value = key
    sortAsc.value = true
  }
}

const filteredAccounts = computed(() => {
  if (!accounts.value) return []
  let list = accounts.value

  if (typeFilter.value !== 'all') {
    list = list.filter(a => a.account_type === typeFilter.value)
  }

  if (searchQuery.value.trim()) {
    const q = searchQuery.value.trim().toLowerCase()
    list = list.filter(a =>
      a.code.toLowerCase().includes(q) ||
      a.name.toLowerCase().includes(q) ||
      a.description.toLowerCase().includes(q),
    )
  }

  const dir = sortAsc.value ? 1 : -1
  return [...list].sort((a, b) => {
    const aVal = a[sortKey.value] ?? ''
    const bVal = b[sortKey.value] ?? ''
    if (aVal < bVal) return -1 * dir
    if (aVal > bVal) return 1 * dir
    return 0
  })
})

const editingId = ref<string | null>(null)
const editForm = ref({ name: '', description: '', mva_eligible: '' })

function startEdit(account: Account) {
  if (account.is_system) return
  editingId.value = account.id
  editForm.value = {
    name: account.name,
    description: account.description,
    mva_eligible: account.mva_eligible,
  }
}

function cancelEdit() {
  editingId.value = null
}

function saveEdit(id: string) {
  updateMutation.mutate({ id, ...editForm.value }, {
    onSuccess: () => { editingId.value = null },
  })
}

const showAddForm = ref(false)
const newAccount = ref({
  code: '',
  name: '',
  account_type: 'expense',
  parent_code: '',
  mva_eligible: 'not_applicable',
  description: '',
  sort_order: 999,
})

function resetNewAccount() {
  newAccount.value = {
    code: '',
    name: '',
    account_type: 'expense',
    parent_code: '',
    mva_eligible: 'not_applicable',
    description: '',
    sort_order: 999,
  }
}

function handleAddAccount() {
  createMutation.mutate(newAccount.value, {
    onSuccess: () => {
      showAddForm.value = false
      resetNewAccount()
    },
  })
}
</script>

<template>
  <div>
    <div class="flex items-center justify-between">
      <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.accounting.accounts.title') }}</h1>
      <button
        v-if="hasAccounts"
        class="inline-flex items-center gap-1.5 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700"
        @click="showAddForm = !showAddForm"
      >
        <Plus class="h-4 w-4" />
        {{ t('admin.accounting.accounts.addAccount') }}
      </button>
    </div>

    <div v-if="isLoading" class="mt-6 text-gray-500">{{ t('common.loading') }}...</div>
    <div v-else-if="isError" class="mt-6 rounded-md bg-red-50 p-3 text-sm text-red-800">{{ t('common.error') }}</div>

    <div v-else-if="!hasAccounts" class="mt-6 rounded-lg border border-dashed border-gray-300 p-8 text-center">
      <h3 class="text-sm font-semibold text-gray-900">{{ t('admin.accounting.accounts.title') }}</h3>
      <p class="mt-1 text-sm text-gray-500">{{ t('admin.accounting.accounts.seedButton') }}</p>
      <button
        class="mt-3 inline-flex items-center gap-1.5 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700"
        :disabled="seedMutation.isPending.value"
        @click="seedMutation.mutate(undefined)"
      >
        {{ t('admin.accounting.accounts.seedButton') }}
      </button>
      <p v-if="seedMutation.isSuccess.value" class="mt-2 text-sm text-green-600">
        {{ seedMutation.data.value?.seeded }} kontoer opprettet
      </p>
    </div>

    <template v-else>
      <div v-if="showAddForm" class="mt-4 rounded-lg border border-gray-200 bg-gray-50 p-4">
        <h3 class="mb-3 text-sm font-semibold text-gray-700">{{ t('admin.accounting.accounts.addAccount') }}</h3>
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-6">
          <input v-model="newAccount.code" :placeholder="t('admin.accounting.accounts.code')" class="rounded-md border border-gray-300 px-3 py-2 text-sm" />
          <input v-model="newAccount.name" :placeholder="t('admin.accounting.accounts.name')" class="rounded-md border border-gray-300 px-3 py-2 text-sm sm:col-span-2" />
          <select v-model="newAccount.account_type" class="rounded-md border border-gray-300 px-3 py-2 text-sm">
            <option value="asset">{{ t('admin.accounting.accounts.typeAsset') }}</option>
            <option value="liability">{{ t('admin.accounting.accounts.typeLiability') }}</option>
            <option value="revenue">{{ t('admin.accounting.accounts.typeRevenue') }}</option>
            <option value="expense">{{ t('admin.accounting.accounts.typeExpense') }}</option>
          </select>
          <select v-model="newAccount.mva_eligible" class="rounded-md border border-gray-300 px-3 py-2 text-sm">
            <option value="not_applicable">{{ t('admin.accounting.accounts.mvaNA') }}</option>
            <option value="eligible">{{ t('admin.accounting.accounts.mvaEligible') }}</option>
            <option value="ineligible">{{ t('admin.accounting.accounts.mvaIneligible') }}</option>
            <option value="partial">{{ t('admin.accounting.accounts.mvaPartial') }}</option>
          </select>
          <input v-model="newAccount.description" :placeholder="t('admin.accounting.accounts.description')" class="rounded-md border border-gray-300 px-3 py-2 text-sm" />
        </div>
        <div class="mt-3 flex gap-2">
          <button
            class="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
            :disabled="!newAccount.code || !newAccount.name || createMutation.isPending.value"
            @click="handleAddAccount"
          >
            {{ t('admin.accounting.accounts.save') }}
          </button>
          <button class="rounded-md border border-gray-300 px-4 py-2 text-sm text-gray-700 hover:bg-gray-100" @click="showAddForm = false">
            {{ t('admin.accounting.accounts.cancel') }}
          </button>
        </div>
        <p v-if="createMutation.isError.value" class="mt-2 text-sm text-red-600">
          {{ (createMutation.error.value as Error)?.message }}
        </p>
      </div>

      <!-- Filters -->
      <div class="mt-4 flex flex-wrap items-center gap-3">
        <input
          v-model="searchQuery"
          type="text"
          :placeholder="t('admin.accounting.accounts.search')"
          class="w-64 rounded-md border border-gray-300 px-3 py-2 text-sm"
        />
        <select v-model="typeFilter" class="rounded-md border border-gray-300 px-3 py-2 text-sm">
          <option v-for="opt in typeFilterOptions" :key="opt" :value="opt">
            {{ opt === 'all' ? t('admin.accounting.journal.allStatuses') : typeLabels[opt] }}
          </option>
        </select>
        <span class="text-sm text-gray-500">
          {{ filteredAccounts.length }} / {{ accounts?.length ?? 0 }}
        </span>
      </div>

      <!-- Single table -->
      <div class="mt-4 overflow-x-auto">
        <table class="min-w-full divide-y divide-gray-200">
          <thead class="bg-gray-50">
            <tr>
              <th
                class="cursor-pointer select-none px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500 hover:text-gray-700"
                @click="toggleSort('code')"
              >
                <span class="inline-flex items-center gap-1">
                  {{ t('admin.accounting.accounts.code') }}
                  <ChevronUp v-if="sortKey === 'code' && sortAsc" class="h-3.5 w-3.5" />
                  <ChevronDown v-else-if="sortKey === 'code' && !sortAsc" class="h-3.5 w-3.5" />
                </span>
              </th>
              <th
                class="cursor-pointer select-none px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500 hover:text-gray-700"
                @click="toggleSort('name')"
              >
                <span class="inline-flex items-center gap-1">
                  {{ t('admin.accounting.accounts.name') }}
                  <ChevronUp v-if="sortKey === 'name' && sortAsc" class="h-3.5 w-3.5" />
                  <ChevronDown v-else-if="sortKey === 'name' && !sortAsc" class="h-3.5 w-3.5" />
                </span>
              </th>
              <th
                class="cursor-pointer select-none px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500 hover:text-gray-700"
                @click="toggleSort('account_type')"
              >
                <span class="inline-flex items-center gap-1">
                  {{ t('admin.accounting.accounts.type') }}
                  <ChevronUp v-if="sortKey === 'account_type' && sortAsc" class="h-3.5 w-3.5" />
                  <ChevronDown v-else-if="sortKey === 'account_type' && !sortAsc" class="h-3.5 w-3.5" />
                </span>
              </th>
              <th
                class="cursor-pointer select-none px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500 hover:text-gray-700"
                @click="toggleSort('mva_eligible')"
              >
                <span class="inline-flex items-center gap-1">
                  {{ t('admin.accounting.accounts.mvaStatus') }}
                  <ChevronUp v-if="sortKey === 'mva_eligible' && sortAsc" class="h-3.5 w-3.5" />
                  <ChevronDown v-else-if="sortKey === 'mva_eligible' && !sortAsc" class="h-3.5 w-3.5" />
                </span>
              </th>
              <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.accounting.accounts.description') }}</th>
              <th class="w-16 px-4 py-3"></th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-200 bg-white">
            <tr
              v-for="account in filteredAccounts"
              :key="account.id"
              :class="{ 'cursor-pointer hover:bg-gray-50': !account.is_system }"
              @click="startEdit(account)"
            >
              <template v-if="editingId === account.id">
                <td class="whitespace-nowrap px-4 py-3">
                  <AccountCodeChip :code="account.code" :account-type="account.account_type" />
                </td>
                <td class="px-4 py-3">
                  <input v-model="editForm.name" class="w-full rounded border border-gray-300 px-2 py-1 text-sm" @click.stop />
                </td>
                <td class="px-4 py-3 text-sm text-gray-500">{{ typeLabels[account.account_type] }}</td>
                <td class="px-4 py-3">
                  <select v-model="editForm.mva_eligible" class="rounded border border-gray-300 px-2 py-1 text-sm" @click.stop>
                    <option value="not_applicable">{{ t('admin.accounting.accounts.mvaNA') }}</option>
                    <option value="eligible">{{ t('admin.accounting.accounts.mvaEligible') }}</option>
                    <option value="ineligible">{{ t('admin.accounting.accounts.mvaIneligible') }}</option>
                    <option value="partial">{{ t('admin.accounting.accounts.mvaPartial') }}</option>
                  </select>
                </td>
                <td class="px-4 py-3">
                  <input v-model="editForm.description" class="w-full rounded border border-gray-300 px-2 py-1 text-sm" @click.stop />
                </td>
                <td class="whitespace-nowrap px-4 py-3">
                  <div class="flex gap-1">
                    <button class="rounded p-1 text-green-600 hover:bg-green-50" @click.stop="saveEdit(account.id)">
                      <Check class="h-4 w-4" />
                    </button>
                    <button class="rounded p-1 text-gray-400 hover:bg-gray-100" @click.stop="cancelEdit">
                      <X class="h-4 w-4" />
                    </button>
                  </div>
                </td>
              </template>
              <template v-else>
                <td class="whitespace-nowrap px-4 py-3">
                  <AccountCodeChip :code="account.code" :account-type="account.account_type" :is-system="account.is_system" />
                </td>
                <td class="px-4 py-3 text-sm text-gray-900">{{ account.name }}</td>
                <td class="px-4 py-3 text-sm text-gray-500">{{ typeLabels[account.account_type] }}</td>
                <td class="px-4 py-3 text-sm text-gray-500">{{ mvaLabels[account.mva_eligible] ?? account.mva_eligible }}</td>
                <td class="px-4 py-3 text-sm text-gray-500">{{ account.description }}</td>
                <td class="px-4 py-3"></td>
              </template>
            </tr>
          </tbody>
        </table>
        <p v-if="!filteredAccounts.length" class="mt-4 text-center text-sm text-gray-500">
          {{ t('admin.accounting.journal.noEntries') }}
        </p>
      </div>
    </template>
  </div>
</template>
