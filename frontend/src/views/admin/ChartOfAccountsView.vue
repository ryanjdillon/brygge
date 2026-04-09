<script setup lang="ts">
import { ref, computed } from 'vue'
import { Lock, Plus, Check, X } from 'lucide-vue-next'
import {
  useAccountsList,
  useSeedAccounts,
  useCreateAccount,
  useUpdateAccount,
  type Account,
} from '@/composables/useAccounting'

const { data: accounts, isLoading, isError } = useAccountsList()
const seedMutation = useSeedAccounts()
const createMutation = useCreateAccount()
const updateMutation = useUpdateAccount()

const hasAccounts = computed(() => (accounts.value?.length ?? 0) > 0)

const typeLabels: Record<string, string> = {
  asset: 'Eiendeler',
  liability: 'Gjeld',
  revenue: 'Inntekter',
  expense: 'Kostnader',
}

const typeOrder = ['asset', 'liability', 'revenue', 'expense']

const mvaLabels: Record<string, string> = {
  eligible: 'Kvalifisert',
  ineligible: 'Ikke kvalifisert',
  partial: 'Delvis',
  not_applicable: 'Ikke aktuelt',
}

const grouped = computed(() => {
  if (!accounts.value) return []
  return typeOrder
    .map(type => ({
      type,
      label: typeLabels[type] ?? type,
      accounts: accounts.value!.filter(a => a.account_type === type),
    }))
    .filter(g => g.accounts.length > 0)
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
      <h1 class="text-2xl font-bold text-gray-900">Kontoplan</h1>
      <button
        v-if="hasAccounts"
        class="inline-flex items-center gap-1.5 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700"
        @click="showAddForm = !showAddForm"
      >
        <Plus class="h-4 w-4" />
        Legg til konto
      </button>
    </div>

    <div v-if="isLoading" class="mt-6 text-gray-500">Laster...</div>
    <div v-else-if="isError" class="mt-6 rounded-md bg-red-50 p-3 text-sm text-red-800">Kunne ikke laste kontoplan</div>

    <div v-else-if="!hasAccounts" class="mt-6 rounded-lg border border-dashed border-gray-300 p-8 text-center">
      <h3 class="text-sm font-semibold text-gray-900">Ingen kontoer</h3>
      <p class="mt-1 text-sm text-gray-500">Opprett standard norsk kontoplan for båtforening.</p>
      <button
        class="mt-3 inline-flex items-center gap-1.5 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700"
        :disabled="seedMutation.isPending.value"
        @click="seedMutation.mutate(undefined)"
      >
        Opprett standard kontoplan
      </button>
      <p v-if="seedMutation.isSuccess.value" class="mt-2 text-sm text-green-600">
        {{ seedMutation.data.value?.seeded }} kontoer opprettet
      </p>
    </div>

    <template v-else>
      <div v-if="showAddForm" class="mt-4 rounded-lg border border-gray-200 bg-gray-50 p-4">
        <h3 class="mb-3 text-sm font-semibold text-gray-700">Ny konto</h3>
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-6">
          <input v-model="newAccount.code" placeholder="Kode (f.eks. 6150)" class="rounded-md border border-gray-300 px-3 py-2 text-sm" />
          <input v-model="newAccount.name" placeholder="Navn" class="rounded-md border border-gray-300 px-3 py-2 text-sm sm:col-span-2" />
          <select v-model="newAccount.account_type" class="rounded-md border border-gray-300 px-3 py-2 text-sm">
            <option value="asset">Eiendeler</option>
            <option value="liability">Gjeld</option>
            <option value="revenue">Inntekter</option>
            <option value="expense">Kostnader</option>
          </select>
          <select v-model="newAccount.mva_eligible" class="rounded-md border border-gray-300 px-3 py-2 text-sm">
            <option value="not_applicable">Ikke aktuelt</option>
            <option value="eligible">Kvalifisert</option>
            <option value="ineligible">Ikke kvalifisert</option>
            <option value="partial">Delvis</option>
          </select>
          <input v-model="newAccount.description" placeholder="Beskrivelse" class="rounded-md border border-gray-300 px-3 py-2 text-sm" />
        </div>
        <div class="mt-3 flex gap-2">
          <button
            class="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
            :disabled="!newAccount.code || !newAccount.name || createMutation.isPending.value"
            @click="handleAddAccount"
          >
            Lagre
          </button>
          <button class="rounded-md border border-gray-300 px-4 py-2 text-sm text-gray-700 hover:bg-gray-100" @click="showAddForm = false">
            Avbryt
          </button>
        </div>
        <p v-if="createMutation.isError.value" class="mt-2 text-sm text-red-600">
          {{ (createMutation.error.value as Error)?.message }}
        </p>
      </div>

      <div v-for="group in grouped" :key="group.type" class="mt-6">
        <h2 class="mb-2 text-lg font-semibold text-gray-800">{{ group.label }}</h2>
        <div class="overflow-x-auto">
          <table class="min-w-full divide-y divide-gray-200">
            <thead class="bg-gray-50">
              <tr>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">Konto</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">Navn</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">Type</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">MVA-status</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">Beskrivelse</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500"></th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-200 bg-white">
              <tr
                v-for="account in group.accounts"
                :key="account.id"
                :class="{ 'cursor-pointer hover:bg-gray-50': !account.is_system }"
                @click="startEdit(account)"
              >
                <template v-if="editingId === account.id">
                  <td class="whitespace-nowrap px-4 py-3 text-sm font-mono text-gray-900">{{ account.code }}</td>
                  <td class="px-4 py-3">
                    <input v-model="editForm.name" class="w-full rounded border border-gray-300 px-2 py-1 text-sm" @click.stop />
                  </td>
                  <td class="px-4 py-3 text-sm text-gray-500">{{ typeLabels[account.account_type] }}</td>
                  <td class="px-4 py-3">
                    <select v-model="editForm.mva_eligible" class="rounded border border-gray-300 px-2 py-1 text-sm" @click.stop>
                      <option value="not_applicable">Ikke aktuelt</option>
                      <option value="eligible">Kvalifisert</option>
                      <option value="ineligible">Ikke kvalifisert</option>
                      <option value="partial">Delvis</option>
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
                  <td class="whitespace-nowrap px-4 py-3 text-sm font-mono text-gray-900">
                    {{ account.code }}
                    <Lock v-if="account.is_system" class="ml-1 inline h-3.5 w-3.5 text-gray-400" />
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
        </div>
      </div>
    </template>
  </div>
</template>
