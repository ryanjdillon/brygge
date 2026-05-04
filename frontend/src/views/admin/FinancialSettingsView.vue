<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ArrowLeft, Save } from 'lucide-vue-next'
import { useTotpGateStore } from '@/stores/totpGate'
import { useAuthStore } from '@/stores/auth'

const { t } = useI18n()
const auth = useAuthStore()
const totpGate = useTotpGateStore()

const clubName = ref('')
const orgNumber = ref('')
const address = ref('')
const bankAccount = ref('')
const loading = ref(true)
const saving = ref(false)
const error = ref<string | null>(null)
const savedAt = ref<Date | null>(null)

async function ensureFreshTotp(): Promise<boolean> {
  if (auth.hasFreshTotp) return true
  return totpGate.open()
}

async function load() {
  loading.value = true
  error.value = null
  try {
    const res = await fetch('/api/v1/admin/settings/financials', { credentials: 'include' })
    if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
    const body = await res.json()
    clubName.value = body.name ?? ''
    orgNumber.value = body.org_number ?? ''
    address.value = body.address ?? ''
    bankAccount.value = body.bank_account ?? ''
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    loading.value = false
  }
}

onMounted(load)

async function save() {
  if (!(await ensureFreshTotp())) return
  saving.value = true
  error.value = null
  try {
    const res = await fetch('/api/v1/admin/settings/financials', {
      method: 'PATCH',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        org_number: orgNumber.value,
        address: address.value,
        bank_account: bankAccount.value,
      }),
    })
    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      throw new Error(`${res.status} ${txt}`)
    }
    savedAt.value = new Date()
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div>
    <div class="mb-3 flex items-center gap-2">
      <RouterLink to="/admin/accounting" class="text-sm text-gray-600 hover:text-gray-900">
        <ArrowLeft class="inline h-4 w-4" /> {{ t('admin.accounting.title') }}
      </RouterLink>
    </div>

    <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.financialSettings.title') }}</h1>
    <p class="mt-1 text-sm text-gray-600">{{ t('admin.financialSettings.subtitle') }}</p>

    <p v-if="loading" class="mt-6 text-sm text-gray-500">{{ t('common.loading') }}…</p>

    <form v-else class="mt-6 max-w-xl space-y-4" @submit.prevent="save">
      <div>
        <label class="block text-xs font-medium text-gray-700">{{ t('admin.financialSettings.clubName') }}</label>
        <input
          :value="clubName"
          disabled
          class="mt-1 w-full rounded-md border border-gray-200 bg-gray-50 px-2 py-1 text-sm text-gray-500"
        />
        <p class="mt-1 text-xs text-gray-500">{{ t('admin.financialSettings.clubNameHint') }}</p>
      </div>

      <div>
        <label class="block text-xs font-medium text-gray-700">{{ t('admin.financialSettings.orgNumber') }}</label>
        <input
          v-model="orgNumber"
          type="text"
          class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm"
          placeholder="999 999 999"
        />
      </div>

      <div>
        <label class="block text-xs font-medium text-gray-700">{{ t('admin.financialSettings.address') }}</label>
        <textarea
          v-model="address"
          rows="2"
          class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm"
          placeholder="Brygga 1, 5378 Klokkarvik"
        />
      </div>

      <div>
        <label class="block text-xs font-medium text-gray-700">{{ t('admin.financialSettings.bankAccount') }}</label>
        <input
          v-model="bankAccount"
          type="text"
          class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm font-mono"
          placeholder="1234.56.78901"
        />
        <p class="mt-1 text-xs text-gray-500">{{ t('admin.financialSettings.bankAccountHint') }}</p>
      </div>

      <p v-if="error" class="rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">{{ error }}</p>
      <p v-else-if="savedAt" class="rounded-md bg-green-50 px-3 py-2 text-sm text-green-700">
        {{ t('admin.financialSettings.saved') }} ({{ savedAt.toLocaleTimeString() }})
      </p>

      <div class="flex justify-end pt-2">
        <button
          type="submit"
          :disabled="saving"
          class="inline-flex items-center gap-1 rounded-md bg-blue-600 px-3 py-1.5 text-sm font-semibold text-white hover:bg-blue-700 disabled:opacity-50"
        >
          <Save class="h-4 w-4" />
          {{ saving ? t('common.loading') : t('common.save') }}
        </button>
      </div>
    </form>
  </div>
</template>
