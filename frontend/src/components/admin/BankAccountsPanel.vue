<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { CheckCircle2, Plus, Trash2 } from 'lucide-vue-next'
import { useFreshTotp } from '@/composables/useFreshTotp'
import { useConfirm } from '@/stores/confirm'

type BankAccountRole = 'drift' | 'hoyrente' | 'other'

interface BankAccount {
  id: string
  account_number: string
  role: BankAccountRole
  gl_code: string
  label: string
  is_default_for_invoices: boolean
  created_at: string
}

interface BankAccountForm {
  account_number: string
  role: BankAccountRole
  gl_code: string
  label: string
  is_default_for_invoices: boolean
}

// Defaults align with the NS 4102 chart of accounts as seeded by
// kontoplan.DefaultKontoplan(): 1920 = Bankkonto drift,
// 1925 = Bankkonto høyrente. "other" stays at 1940 (Andre
// bankinnskudd) as a generic fallback — operators using a different
// code can override.
const ROLE_GL_DEFAULTS: Record<BankAccountRole, string> = {
  drift: '1920',
  hoyrente: '1925',
  other: '1940',
}

const { t } = useI18n()
const { totpAwareFetch } = useFreshTotp()
const askConfirm = useConfirm()
const queryClient = useQueryClient()

const error = ref<string | null>(null)
const editingId = ref<string | null>(null)
const form = ref<BankAccountForm>(blankForm())
const showForm = ref(false)

function blankForm(): BankAccountForm {
  return {
    account_number: '',
    role: 'drift',
    gl_code: ROLE_GL_DEFAULTS.drift,
    label: '',
    is_default_for_invoices: false,
  }
}

watch(
  () => form.value.role,
  (newRole, oldRole) => {
    if (newRole === oldRole) return
    if (form.value.gl_code === ROLE_GL_DEFAULTS[oldRole]) {
      form.value.gl_code = ROLE_GL_DEFAULTS[newRole]
    }
  },
)

const { data: accounts, isLoading } = useQuery<BankAccount[]>({
  queryKey: ['admin-bank-accounts'],
  queryFn: async () => {
    const res = await fetch('/api/v1/admin/settings/bank-accounts', { credentials: 'include' })
    if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
    return (await res.json()) as BankAccount[]
  },
})

const hasDefault = computed(() => (accounts.value ?? []).some((a) => a.is_default_for_invoices))

function openCreate() {
  editingId.value = null
  form.value = blankForm()
  if (!hasDefault.value) form.value.is_default_for_invoices = true
  showForm.value = true
  error.value = null
}

function openEdit(a: BankAccount) {
  editingId.value = a.id
  form.value = {
    account_number: a.account_number,
    role: a.role,
    gl_code: a.gl_code,
    label: a.label,
    is_default_for_invoices: a.is_default_for_invoices,
  }
  showForm.value = true
  error.value = null
}

function cancel() {
  showForm.value = false
  editingId.value = null
}

const { mutateAsync: save, isPending: saving } = useMutation({
  mutationFn: async () => {
    const url = editingId.value
      ? `/api/v1/admin/settings/bank-accounts/${editingId.value}`
      : '/api/v1/admin/settings/bank-accounts'
    const method = editingId.value ? 'PUT' : 'POST'
    const res = await totpAwareFetch(url, {
      method,
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(form.value),
    })
    if (!res.ok) {
      const body = await res.json().catch(() => ({ error: res.statusText }))
      throw new Error(body.error ?? res.statusText)
    }
    return res.json()
  },
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['admin-bank-accounts'] })
    showForm.value = false
    editingId.value = null
  },
  onError: (e: Error) => {
    error.value = e.message
  },
})

const { mutateAsync: archive } = useMutation({
  mutationFn: async (id: string) => {
    const res = await totpAwareFetch(`/api/v1/admin/settings/bank-accounts/${id}`, {
      method: 'DELETE',
    })
    if (!res.ok) {
      const body = await res.json().catch(() => ({ error: res.statusText }))
      throw new Error(body.error ?? res.statusText)
    }
  },
  onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admin-bank-accounts'] }),
  onError: (e: Error) => {
    error.value = e.message
  },
})

async function confirmArchive(a: BankAccount) {
  const ok = await askConfirm({
    title: t('admin.bankAccounts.confirmArchiveTitle'),
    body: t('admin.bankAccounts.confirmArchive', { account: a.account_number }),
    confirmLabel: t('admin.bankAccounts.archiveAction'),
    tone: 'danger',
  })
  if (!ok) return
  await archive(a.id)
}
</script>

<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between gap-3">
      <p class="text-sm text-gray-600">{{ t('admin.bankAccounts.intro') }}</p>
      <button
        type="button"
        class="inline-flex shrink-0 items-center gap-2 rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white hover:bg-blue-700"
        @click="openCreate"
      >
        <Plus class="h-4 w-4" />
        {{ t('admin.bankAccounts.add') }}
      </button>
    </div>

    <div v-if="error" class="rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">{{ error }}</div>

    <div v-if="isLoading" class="animate-pulse space-y-2">
      <div class="h-14 rounded bg-gray-100" />
      <div class="h-14 rounded bg-gray-100" />
    </div>

    <ul v-else-if="(accounts?.length ?? 0) > 0" class="divide-y divide-gray-200 rounded-md border border-gray-200 bg-white">
      <li v-for="a in accounts" :key="a.id" class="flex items-center gap-3 px-4 py-3">
        <div class="flex-1">
          <div class="flex items-center gap-2 font-mono text-sm font-semibold text-gray-900">
            {{ a.account_number }}
            <span
              v-if="a.is_default_for_invoices"
              class="inline-flex items-center gap-1 rounded bg-green-50 px-2 py-0.5 text-xs font-semibold text-green-700"
            >
              <CheckCircle2 class="h-3.5 w-3.5" />
              {{ t('admin.bankAccounts.fakturaDefault') }}
            </span>
          </div>
          <div class="text-xs text-gray-500">
            {{ t(`admin.bankAccounts.role.${a.role}`) }}
            · {{ t('admin.bankAccounts.glCode') }} {{ a.gl_code }}
            <span v-if="a.label">· {{ a.label }}</span>
          </div>
        </div>
        <button
          type="button"
          class="text-sm font-medium text-blue-600 hover:text-blue-700"
          @click="openEdit(a)"
        >
          {{ t('common.edit') }}
        </button>
        <button
          type="button"
          class="rounded p-1.5 text-gray-400 hover:bg-red-50 hover:text-red-600 disabled:opacity-40 disabled:hover:bg-transparent disabled:hover:text-gray-400"
          :disabled="a.is_default_for_invoices"
          :title="a.is_default_for_invoices ? t('admin.bankAccounts.cannotArchiveDefault') : t('admin.bankAccounts.archive')"
          @click="confirmArchive(a)"
        >
          <Trash2 class="h-4 w-4" />
        </button>
      </li>
    </ul>

    <p v-else class="rounded-md border border-dashed border-gray-300 px-4 py-6 text-center text-sm text-gray-500">
      {{ t('admin.bankAccounts.empty') }}
    </p>

    <form
      v-if="showForm"
      class="space-y-4 rounded-md border border-gray-200 bg-white p-4"
      @submit.prevent="save()"
    >
      <h2 class="text-lg font-semibold text-gray-900">
        {{ editingId ? t('admin.bankAccounts.editTitle') : t('admin.bankAccounts.addTitle') }}
      </h2>

      <div>
        <label class="block text-sm font-medium text-gray-700">{{ t('admin.bankAccounts.accountNumber') }}</label>
        <input
          v-model="form.account_number"
          class="mt-1 w-full rounded-md border border-gray-300 px-3 py-2 font-mono text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          placeholder="1234.56.78901"
          required
        />
      </div>

      <div class="grid grid-cols-2 gap-4">
        <div>
          <label class="block text-sm font-medium text-gray-700">{{ t('admin.bankAccounts.roleLabel') }}</label>
          <select
            v-model="form.role"
            class="mt-1 w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          >
            <option value="drift">{{ t('admin.bankAccounts.role.drift') }}</option>
            <option value="hoyrente">{{ t('admin.bankAccounts.role.hoyrente') }}</option>
            <option value="other">{{ t('admin.bankAccounts.role.other') }}</option>
          </select>
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700">{{ t('admin.bankAccounts.glCode') }}</label>
          <input
            v-model="form.gl_code"
            class="mt-1 w-full rounded-md border border-gray-300 px-3 py-2 font-mono text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            placeholder="1920"
          />
        </div>
      </div>

      <div>
        <label class="block text-sm font-medium text-gray-700">{{ t('admin.bankAccounts.labelOptional') }}</label>
        <input
          v-model="form.label"
          class="mt-1 w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          :placeholder="t('admin.bankAccounts.labelPlaceholder')"
        />
      </div>

      <label class="flex items-center gap-2 text-sm text-gray-700">
        <input v-model="form.is_default_for_invoices" type="checkbox" class="rounded border-gray-300" />
        {{ t('admin.bankAccounts.useForFakturas') }}
      </label>

      <div class="flex justify-end gap-2 pt-2">
        <button
          type="button"
          class="rounded-md bg-white px-4 py-2 text-sm font-semibold text-gray-700 ring-1 ring-gray-300 hover:bg-gray-50"
          @click="cancel"
        >
          {{ t('common.cancel') }}
        </button>
        <button
          type="submit"
          :disabled="saving"
          class="rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white hover:bg-blue-700 disabled:opacity-50"
        >
          {{ saving ? t('common.loading') : t('common.save') }}
        </button>
      </div>
    </form>
  </div>
</template>
