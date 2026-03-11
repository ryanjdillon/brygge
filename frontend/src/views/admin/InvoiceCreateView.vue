<script setup lang="ts">
import { ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useQuery } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'
import { useCreateInvoice } from '@/composables/useFinancials'
import { FilePlus, Search } from 'lucide-vue-next'

const { t } = useI18n()
const router = useRouter()
const client = useApiClient()

interface UserOption {
  id: string
  full_name: string
  email: string
}

const searchQuery = ref('')
const selectedUserId = ref('')
const selectedUserName = ref('')
const invoiceType = ref('dues')
const amount = ref<number | undefined>(undefined)
const description = ref('')
const dueDate = ref('')
const showUserSearch = ref(false)
const successMessage = ref('')

const { data: usersResponse } = useQuery({
  queryKey: ['admin-users-search'],
  queryFn: async () => unwrap(await client.GET('/api/v1/admin/users')),
  staleTime: 5 * 60 * 1000,
})

const filteredUsers = computed(() => {
  const userList = usersResponse.value?.users
  if (!userList || !searchQuery.value) return []
  const q = searchQuery.value.toLowerCase()
  return (userList as UserOption[]).filter(
    (u) => u.full_name.toLowerCase().includes(q) || u.email.toLowerCase().includes(q),
  ).slice(0, 10)
})

const { mutate: createInvoice, isPending } = useCreateInvoice()

function selectUser(user: UserOption) {
  selectedUserId.value = user.id
  selectedUserName.value = user.full_name
  searchQuery.value = user.full_name
  showUserSearch.value = false
}

const canSubmit = computed(() =>
  selectedUserId.value && invoiceType.value && amount.value && amount.value > 0 && dueDate.value,
)

function handleSubmit() {
  if (!canSubmit.value || !amount.value) return

  createInvoice(
    {
      user_id: selectedUserId.value,
      type: invoiceType.value,
      amount: amount.value,
      description: description.value,
      due_date: dueDate.value,
    },
    {
      onSuccess: () => {
        successMessage.value = t('admin.financials.invoiceCreated')
        setTimeout(() => {
          router.push('/admin/financials/payments')
        }, 1500)
      },
    },
  )
}
</script>

<template>
  <div class="mx-auto max-w-xl">
    <div class="flex items-center gap-3">
      <FilePlus class="h-6 w-6 text-green-600" />
      <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.financials.createInvoice') }}</h1>
    </div>

    <div
      v-if="successMessage"
      class="mt-6 rounded-md bg-green-50 p-4 text-sm font-medium text-green-800"
    >
      {{ successMessage }}
    </div>

    <form class="mt-6 space-y-5" @submit.prevent="handleSubmit">
      <div class="relative">
        <label class="block text-sm font-medium text-gray-700">{{ t('admin.financials.selectMember') }}</label>
        <div class="relative mt-1">
          <Search class="absolute left-3 top-2.5 h-4 w-4 text-gray-400" />
          <input
            v-model="searchQuery"
            type="text"
            :placeholder="t('admin.financials.searchMember')"
            class="block w-full rounded-md border border-gray-300 py-2 pl-9 pr-3 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            @focus="showUserSearch = true"
            @input="showUserSearch = true"
          />
        </div>
        <ul
          v-if="showUserSearch && filteredUsers.length > 0"
          class="absolute z-10 mt-1 max-h-48 w-full overflow-auto rounded-md border border-gray-200 bg-white shadow-lg"
        >
          <li
            v-for="user in filteredUsers"
            :key="user.id"
            class="cursor-pointer px-4 py-2 text-sm hover:bg-blue-50"
            @mousedown="selectUser(user)"
          >
            <span class="font-medium text-gray-900">{{ user.full_name }}</span>
            <span class="ml-2 text-gray-500">{{ user.email }}</span>
          </li>
        </ul>
      </div>

      <div>
        <label class="block text-sm font-medium text-gray-700">{{ t('admin.financials.paymentType') }}</label>
        <select
          v-model="invoiceType"
          class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        >
          <option value="dues">{{ t('admin.financials.typeDues') }}</option>
          <option value="andel">{{ t('admin.financials.typeAndel') }}</option>
          <option value="slip_fee">{{ t('admin.financials.typeSlipFee') }}</option>
          <option value="booking">{{ t('admin.financials.typeBooking') }}</option>
          <option value="merchandise">{{ t('admin.financials.typeMerchandise') }}</option>
        </select>
      </div>

      <div>
        <label class="block text-sm font-medium text-gray-700">{{ t('admin.financials.amount') }} (NOK)</label>
        <input
          v-model.number="amount"
          type="number"
          min="1"
          step="0.01"
          class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        />
      </div>

      <div>
        <label class="block text-sm font-medium text-gray-700">{{ t('admin.financials.description') }}</label>
        <textarea
          v-model="description"
          rows="3"
          class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        />
      </div>

      <div>
        <label class="block text-sm font-medium text-gray-700">{{ t('admin.financials.dueDate') }}</label>
        <input
          v-model="dueDate"
          type="date"
          class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        />
      </div>

      <div v-if="selectedUserId" class="rounded-md bg-blue-50 p-3 text-sm text-blue-800">
        {{ t('admin.financials.invoicePreview', { name: selectedUserName, type: invoiceType, amount: amount ?? 0, due: dueDate }) }}
      </div>

      <div class="flex gap-3 pt-2">
        <button
          type="submit"
          :disabled="!canSubmit || isPending"
          class="flex-1 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
        >
          {{ isPending ? t('common.loading') + '...' : t('admin.financials.createInvoice') }}
        </button>
        <button
          type="button"
          class="rounded-md border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
          @click="router.push('/admin/financials')"
        >
          {{ t('common.cancel') }}
        </button>
      </div>
    </form>
  </div>
</template>
