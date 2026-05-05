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
const websiteUrl = ref('')
const chairmanEmail = ref('')
const treasurerEmail = ref('')
const secretaryEmail = ref('')
const harborMasterEmail = ref('')
const hasLogo = ref(false)
const logoMime = ref('')
const logoCacheBust = ref(0)
const logoUploading = ref(false)
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
    websiteUrl.value = body.website_url ?? ''
    chairmanEmail.value = body.chairman_email ?? ''
    treasurerEmail.value = body.treasurer_email ?? ''
    secretaryEmail.value = body.secretary_email ?? ''
    harborMasterEmail.value = body.harbor_master_email ?? ''
    hasLogo.value = !!body.has_logo
    logoMime.value = body.logo_mime ?? ''
    logoCacheBust.value = Date.now()
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    loading.value = false
  }
}

onMounted(load)

async function uploadLogo(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  if (file.type !== 'image/png' && file.type !== 'image/jpeg') {
    error.value = t('admin.financialSettings.logoMimeError')
    input.value = ''
    return
  }
  if (file.size > 2 * 1024 * 1024) {
    error.value = t('admin.financialSettings.logoSizeError')
    input.value = ''
    return
  }
  if (!(await ensureFreshTotp())) {
    input.value = ''
    return
  }
  logoUploading.value = true
  error.value = null
  try {
    const fd = new FormData()
    fd.append('logo', file)
    const res = await fetch('/api/v1/admin/settings/financials/logo', {
      method: 'POST',
      credentials: 'include',
      body: fd,
    })
    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      throw new Error(`${res.status} ${txt}`)
    }
    const body = await res.json()
    hasLogo.value = true
    logoMime.value = body.mime ?? ''
    logoCacheBust.value = Date.now()
  } catch (err) {
    error.value = (err as Error).message
  } finally {
    logoUploading.value = false
    input.value = ''
  }
}

async function deleteLogo() {
  if (!confirm(t('admin.financialSettings.logoDeleteConfirm'))) return
  if (!(await ensureFreshTotp())) return
  error.value = null
  try {
    const res = await fetch('/api/v1/admin/settings/financials/logo', {
      method: 'DELETE',
      credentials: 'include',
    })
    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      throw new Error(`${res.status} ${txt}`)
    }
    hasLogo.value = false
    logoMime.value = ''
    logoCacheBust.value = Date.now()
  } catch (err) {
    error.value = (err as Error).message
  }
}

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
        website_url: websiteUrl.value,
        chairman_email: chairmanEmail.value,
        treasurer_email: treasurerEmail.value,
        secretary_email: secretaryEmail.value,
        harbor_master_email: harborMasterEmail.value,
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
        <label class="block text-xs font-medium text-gray-700">{{ t('admin.financialSettings.logo') }}</label>
        <p class="mt-1 text-xs text-gray-500">{{ t('admin.financialSettings.logoHint') }}</p>
        <div class="mt-2 flex items-center gap-3">
          <img
            v-if="hasLogo"
            :src="`/api/v1/admin/settings/financials/logo?v=${logoCacheBust}`"
            alt="Logo"
            class="h-16 rounded border border-gray-200 bg-white p-1 object-contain"
          />
          <span v-else class="text-xs italic text-gray-400">{{ t('admin.financialSettings.logoNone') }}</span>
        </div>
        <div class="mt-2 flex items-center gap-2">
          <label class="inline-flex cursor-pointer items-center gap-1 rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm text-gray-700 hover:bg-gray-50">
            <input
              type="file"
              accept="image/png,image/jpeg"
              class="hidden"
              :disabled="logoUploading"
              @change="uploadLogo"
            />
            {{ logoUploading ? t('common.loading') : (hasLogo ? t('admin.financialSettings.logoReplace') : t('admin.financialSettings.logoUpload')) }}
          </label>
          <button
            v-if="hasLogo"
            type="button"
            class="rounded-md border border-red-300 bg-white px-3 py-1.5 text-sm text-red-700 hover:bg-red-50"
            @click="deleteLogo"
          >
            {{ t('common.delete') }}
          </button>
        </div>
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

      <div>
        <label class="block text-xs font-medium text-gray-700">{{ t('admin.financialSettings.websiteUrl') }}</label>
        <input
          v-model="websiteUrl"
          type="url"
          class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm"
          placeholder="https://klokkarvikbaatlag.no"
        />
        <p class="mt-1 text-xs text-gray-500">{{ t('admin.financialSettings.websiteUrlHint') }}</p>
      </div>

      <fieldset class="rounded-md border border-gray-200 p-3">
        <legend class="px-1 text-xs font-semibold text-gray-700">{{ t('admin.financialSettings.boardEmails') }}</legend>
        <p class="mb-2 text-xs text-gray-500">{{ t('admin.financialSettings.boardEmailsHint') }}</p>
        <div class="grid gap-3 sm:grid-cols-2">
          <div>
            <label class="block text-xs font-medium text-gray-700">{{ t('admin.financialSettings.chairmanEmail') }}</label>
            <input v-model="chairmanEmail" type="email" class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm" placeholder="leder@klubb.no" />
          </div>
          <div>
            <label class="block text-xs font-medium text-gray-700">{{ t('admin.financialSettings.treasurerEmail') }}</label>
            <input v-model="treasurerEmail" type="email" class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm" placeholder="kasserer@klubb.no" />
            <p class="mt-1 text-xs text-gray-500">{{ t('admin.financialSettings.treasurerEmailHint') }}</p>
          </div>
          <div>
            <label class="block text-xs font-medium text-gray-700">{{ t('admin.financialSettings.secretaryEmail') }}</label>
            <input v-model="secretaryEmail" type="email" class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm" placeholder="sekretaer@klubb.no" />
          </div>
          <div>
            <label class="block text-xs font-medium text-gray-700">{{ t('admin.financialSettings.harborMasterEmail') }}</label>
            <input v-model="harborMasterEmail" type="email" class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm" placeholder="havnesjef@klubb.no" />
          </div>
        </div>
      </fieldset>

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
