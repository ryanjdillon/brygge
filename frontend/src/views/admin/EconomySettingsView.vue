<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useFreshTotp } from '@/composables/useFreshTotp'
import { useCan } from '@/composables/useCan'
import FileInput from '@/components/ui/form/FileInput.vue'
import Tabs from '@/components/ui/Tabs.vue'
import BankAccountsPanel from '@/components/admin/BankAccountsPanel.vue'
import ExemptionsSettingsPanel from '@/components/admin/ExemptionsSettingsPanel.vue'

const { t } = useI18n()
const { ensureFreshTotp, totpAwareFetch } = useFreshTotp()

const activeTab = ref<'faktura' | 'bank-accounts'>('faktura')

const hasFakturaLogo = ref(false)
const fakturaLogoCacheBust = ref(0)
const fakturaLogoUploading = ref(false)
const loading = ref(true)
const error = ref<string | null>(null)

const kidCheckMethod = ref<'mod10' | 'mod11'>('mod11')
const kidSaving = ref(false)
const kidSaved = ref(false)

const vippsClearingAccount = ref('2900')
const paymentFeeAccount = ref('7700')
// Card payments wait on the Stripe clearing account until payout; the
// descriptor is a regular expression for the payout's bank statement text.
const stripeClearingAccount = ref('1580')
const stripePayoutDescriptor = ref('(?i)stripe')
// Max invoice amount (kr) eligible for online payment; '' = no limit (BRY-283).
const onlineMaxAmount = ref<string>('')
// Book unambiguous Vipps charges without review (BRY-340); the tie category
// decides amounts that fit price items in several categories ('' = review).
const vippsAutoAssign = ref(false)
const vippsAutoAssignTieCategory = ref('')
const priceCategories = [
  'membership', 'harbor_membership', 'slip_fee', 'electricity', 'seasonal_rental',
  'guest', 'motorhome', 'room_hire', 'service', 'other',
] as const
function categoryLabel(c: string) {
  return t(`admin.pricing.category${c.split('_').map((p) => p[0].toUpperCase() + p.slice(1)).join('')}`)
}
const vippsSaving = ref(false)
const vippsSaved = ref(false)

// Book unambiguous KID-less bank deposits without review (BRY-366); a
// switch of its own, so turning on Vipps auto-assign never starts booking
// bank payments.
const bankAutoAssign = ref(false)
const bankSaving = ref(false)
const bankSaved = ref(false)

async function load() {
  loading.value = true
  error.value = null
  try {
    const res = await fetch('/api/v1/admin/settings/site', { credentials: 'include' })
    if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
    const body = await res.json()
    hasFakturaLogo.value = !!body.has_faktura_logo
    kidCheckMethod.value = body.kid_check_method === 'mod10' ? 'mod10' : 'mod11'
    vippsClearingAccount.value = body.vipps_clearing_account || '2900'
    paymentFeeAccount.value = body.payment_fee_account || '7700'
    stripeClearingAccount.value = body.stripe_clearing_account || '1580'
    stripePayoutDescriptor.value = body.stripe_payout_descriptor || '(?i)stripe'
    onlineMaxAmount.value = body.online_max_amount != null ? String(body.online_max_amount) : ''
    vippsAutoAssign.value = !!body.vipps_auto_assign
    vippsAutoAssignTieCategory.value = body.vipps_auto_assign_tie_category || ''
    bankAutoAssign.value = !!body.bank_auto_assign
    fakturaLogoCacheBust.value = Date.now()
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    loading.value = false
  }
}

async function saveKidCheckMethod() {
  if (!(await ensureFreshTotp())) return
  kidSaving.value = true
  kidSaved.value = false
  error.value = null
  try {
    const res = await totpAwareFetch('/api/v1/admin/settings/site', {
      method: 'PATCH',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ kid_check_method: kidCheckMethod.value }),
    })
    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      throw new Error(`${res.status} ${txt}`)
    }
    kidSaved.value = true
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    kidSaving.value = false
  }
}

async function saveVippsAccounts() {
  if (!(await ensureFreshTotp())) return
  vippsSaving.value = true
  vippsSaved.value = false
  error.value = null
  try {
    const res = await totpAwareFetch('/api/v1/admin/settings/site', {
      method: 'PATCH',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        vipps_clearing_account: vippsClearingAccount.value.trim(),
        payment_fee_account: paymentFeeAccount.value.trim(),
        stripe_clearing_account: stripeClearingAccount.value.trim(),
        stripe_payout_descriptor: stripePayoutDescriptor.value.trim(),
        // Empty field clears the cap (0 → NULL on the backend); otherwise set it.
        online_max_amount: onlineMaxAmount.value.trim() === '' ? 0 : Number(onlineMaxAmount.value),
        vipps_auto_assign: vippsAutoAssign.value,
        vipps_auto_assign_tie_category: vippsAutoAssignTieCategory.value,
      }),
    })
    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      throw new Error(`${res.status} ${txt}`)
    }
    vippsSaved.value = true
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    vippsSaving.value = false
  }
}

async function saveBankAutoAssign() {
  if (!(await ensureFreshTotp())) return
  bankSaving.value = true
  bankSaved.value = false
  error.value = null
  try {
    const res = await totpAwareFetch('/api/v1/admin/settings/site', {
      method: 'PATCH',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ bank_auto_assign: bankAutoAssign.value }),
    })
    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      throw new Error(`${res.status} ${txt}`)
    }
    bankSaved.value = true
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    bankSaving.value = false
  }
}

async function uploadFakturaLogo(files: FileList | null) {
  const file = files?.[0]
  if (!file) return
  if (file.size > 2 * 1024 * 1024) {
    error.value = t('admin.economySettings.logoSizeError')
    return
  }
  if (!(await ensureFreshTotp())) return
  fakturaLogoUploading.value = true
  error.value = null
  try {
    const fd = new FormData()
    fd.append('logo', file)
    const res = await totpAwareFetch('/api/v1/admin/settings/economy/faktura-logo', {
      method: 'POST',
      credentials: 'include',
      body: fd,
    })
    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      throw new Error(`${res.status} ${txt}`)
    }
    await res.json()
    hasFakturaLogo.value = true
    fakturaLogoCacheBust.value = Date.now()
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    fakturaLogoUploading.value = false
  }
}

async function deleteFakturaLogo() {
  if (!(await ensureFreshTotp())) return
  if (!confirm(t('admin.economySettings.logoDeleteConfirm'))) return
  error.value = null
  try {
    const res = await totpAwareFetch('/api/v1/admin/settings/economy/faktura-logo', {
      method: 'DELETE',
      credentials: 'include',
    })
    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      throw new Error(`${res.status} ${txt}`)
    }
    hasFakturaLogo.value = false
    fakturaLogoCacheBust.value = Date.now()
  } catch (e) {
    error.value = (e as Error).message
  }
}

onMounted(load)

// Board members without the money role read these settings; a disabled
// fieldset turns every control below read-only (BRY-372).
const { can } = useCan()
const canEdit = can('money')
</script>

<template>
  <div class="space-y-6">
    <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.economySettings.title') }}</h1>
    <p class="text-sm text-gray-600">{{ t('admin.economySettings.subtitle') }}</p>

    <Tabs
      v-model="activeTab"
      :tabs="[
        { value: 'faktura', label: t('admin.economySettings.fakturaTab') },
        { value: 'bank-accounts', label: t('admin.economySettings.bankAccountsTab') },
      ]"
    />

    <div v-if="error" class="rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">{{ error }}</div>

    <fieldset :disabled="!canEdit" class="contents" data-testid="economy-settings-fields">
    <section v-if="activeTab === 'faktura'" class="space-y-5">
      <fieldset class="rounded-md border border-slate-200 bg-slate-50 p-3 space-y-3">
        <legend class="px-1 text-xs font-semibold text-slate-700">
          {{ t('admin.economySettings.fakturaLogoLabel') }}
        </legend>
        <p class="text-xs text-slate-600">{{ t('admin.economySettings.fakturaLogoHint') }}</p>
        <div v-if="hasFakturaLogo" class="flex items-center gap-4">
          <img
            :src="`/api/v1/admin/settings/economy/faktura-logo?v=${fakturaLogoCacheBust}`"
            alt=""
            class="h-16 w-auto rounded border border-slate-200 bg-white p-1"
          />
          <button
            type="button"
            class="text-sm font-medium text-red-600 hover:text-red-700"
            @click="deleteFakturaLogo"
          >
            {{ t('common.delete') }}
          </button>
        </div>
        <FileInput
          accept="image/png,image/jpeg"
          :disabled="fakturaLogoUploading"
          @change="uploadFakturaLogo"
        />
      </fieldset>

      <fieldset class="rounded-md border border-slate-200 bg-slate-50 p-3 space-y-3">
        <legend class="px-1 text-xs font-semibold text-slate-700">
          {{ t('admin.economySettings.kidMethodLabel') }}
        </legend>
        <p class="text-xs text-slate-600">{{ t('admin.economySettings.kidMethodHint') }}</p>
        <div class="flex flex-wrap items-center gap-3">
          <select
            v-model="kidCheckMethod"
            class="rounded-md border border-slate-300 bg-white px-3 py-1.5 text-sm text-gray-900 focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
            @change="kidSaved = false"
          >
            <option value="mod11">{{ t('admin.economySettings.kidMethodMod11') }}</option>
            <option value="mod10">{{ t('admin.economySettings.kidMethodMod10') }}</option>
          </select>
          <button
            type="button"
            class="rounded-md bg-brand-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-brand-700 disabled:opacity-50"
            :disabled="kidSaving"
            @click="saveKidCheckMethod"
          >
            {{ t('common.save') }}
          </button>
          <span v-if="kidSaved" class="text-sm font-medium text-green-600">
            {{ t('common.saved') }}
          </span>
        </div>
      </fieldset>

      <fieldset class="rounded-md border border-slate-200 bg-slate-50 p-3 space-y-3">
        <legend class="px-1 text-xs font-semibold text-slate-700">
          {{ t('admin.economySettings.paymentAccountsLabel') }}
        </legend>
        <p class="text-xs text-slate-600">{{ t('admin.economySettings.paymentAccountsHint') }}</p>
        <div class="flex flex-wrap items-end gap-3">
          <label class="flex flex-col gap-1 text-xs text-slate-700">
            {{ t('admin.economySettings.vippsClearingAccount') }}
            <input
              v-model="vippsClearingAccount"
              inputmode="numeric"
              class="w-28 rounded-md border border-slate-300 bg-white px-3 py-1.5 text-sm text-gray-900 focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
              @input="vippsSaved = false"
            />
          </label>
          <label class="flex flex-col gap-1 text-xs text-slate-700">
            {{ t('admin.economySettings.paymentFeeAccount') }}
            <input
              v-model="paymentFeeAccount"
              inputmode="numeric"
              class="w-28 rounded-md border border-slate-300 bg-white px-3 py-1.5 text-sm text-gray-900 focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
              @input="vippsSaved = false"
            />
          </label>
          <label class="flex flex-col gap-1 text-xs text-slate-700">
            {{ t('admin.economySettings.stripeClearingAccount') }}
            <input
              v-model="stripeClearingAccount"
              inputmode="numeric"
              class="w-28 rounded-md border border-slate-300 bg-white px-3 py-1.5 text-sm text-gray-900 focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
              @input="vippsSaved = false"
            />
          </label>
          <label class="flex flex-col gap-1 text-xs text-slate-700">
            {{ t('admin.economySettings.stripePayoutDescriptor') }}
            <input
              v-model="stripePayoutDescriptor"
              maxlength="200"
              spellcheck="false"
              class="w-40 rounded-md border border-slate-300 bg-white px-3 py-1.5 font-mono text-sm text-gray-900 focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
              @input="vippsSaved = false"
            />
          </label>
          <label class="flex flex-col gap-1 text-xs text-slate-700">
            {{ t('admin.economySettings.onlineMaxAmount') }}
            <input
              v-model="onlineMaxAmount"
              inputmode="numeric"
              :placeholder="t('admin.economySettings.onlineMaxAmountPlaceholder')"
              class="w-32 rounded-md border border-slate-300 bg-white px-3 py-1.5 text-sm text-gray-900 focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
              @input="vippsSaved = false"
            />
          </label>
          <button
            type="button"
            class="rounded-md bg-brand-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-brand-700 disabled:opacity-50"
            :disabled="vippsSaving"
            @click="saveVippsAccounts"
          >
            {{ t('common.save') }}
          </button>
          <span v-if="vippsSaved" class="text-sm font-medium text-green-600">
            {{ t('common.saved') }}
          </span>
        </div>
        <p class="text-xs text-slate-500">{{ t('admin.economySettings.stripeAccountsHint') }}</p>
        <p class="text-xs text-slate-500">{{ t('admin.economySettings.onlineMaxAmountHint') }}</p>
        <div class="mt-2 flex flex-wrap items-end gap-3" data-testid="vipps-auto-assign">
          <label class="flex items-center gap-2 text-sm text-slate-700">
            <input v-model="vippsAutoAssign" type="checkbox" @change="vippsSaved = false" />
            {{ t('admin.economySettings.vippsAutoAssign') }}
          </label>
          <label class="flex flex-col gap-1 text-xs text-slate-700">
            {{ t('admin.economySettings.vippsAutoAssignTieCategory') }}
            <select
              v-model="vippsAutoAssignTieCategory"
              :disabled="!vippsAutoAssign"
              class="rounded-md border border-slate-300 bg-white px-3 py-1.5 text-sm text-gray-900 focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
              @change="vippsSaved = false"
            >
              <option value="">{{ t('admin.economySettings.vippsAutoAssignTieNone') }}</option>
              <option v-for="c in priceCategories" :key="c" :value="c">{{ categoryLabel(c) }}</option>
            </select>
          </label>
        </div>
        <p class="text-xs text-slate-500">{{ t('admin.economySettings.vippsAutoAssignHint') }}</p>
      </fieldset>

      <fieldset class="rounded-md border border-slate-200 bg-slate-50 p-3 space-y-3" data-testid="bank-auto-assign">
        <legend class="px-1 text-xs font-semibold text-slate-700">
          {{ t('admin.economySettings.bankAutoAssignLabel') }}
        </legend>
        <div class="flex flex-wrap items-center gap-3">
          <label class="flex items-center gap-2 text-sm text-slate-700">
            <input v-model="bankAutoAssign" type="checkbox" @change="bankSaved = false" />
            {{ t('admin.economySettings.bankAutoAssign') }}
          </label>
          <button
            type="button"
            class="rounded-md bg-brand-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-brand-700 disabled:opacity-50"
            :disabled="bankSaving"
            @click="saveBankAutoAssign"
          >
            {{ t('common.save') }}
          </button>
          <span v-if="bankSaved" class="text-sm font-medium text-green-600">
            {{ t('common.saved') }}
          </span>
        </div>
        <p class="text-xs text-slate-500">{{ t('admin.economySettings.bankAutoAssignHint') }}</p>
      </fieldset>

      <ExemptionsSettingsPanel />
    </section>

    <BankAccountsPanel v-else-if="activeTab === 'bank-accounts'" />
    </fieldset>
  </div>
</template>
