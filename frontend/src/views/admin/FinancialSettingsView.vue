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
const phone = ref('')
const vhfChannel = ref('')
const latitude = ref<string>('')
const longitude = ref<string>('')
const bankAccount = ref('')

const harborApproach = ref('')
const harborDepth = ref('')
const harborVhf = ref('')
const harborCtaTitle = ref('')
const harborCtaDescription = ref('')

const motorhomePower = ref('')
const motorhomeFacilities = ref('')
const motorhomeCheckin = ref('')
const motorhomeRules = ref('')
const motorhomeCtaTitle = ref('')
const motorhomeCtaDescription = ref('')
const websiteUrl = ref('')
const chairmanEmail = ref('')
const viceChairmanEmail = ref('')
const treasurerEmail = ref('')
const secretaryEmail = ref('')
const harborMasterEmail = ref('')
const hasFakturaLogo = ref(false)
const fakturaLogoMime = ref('')
const fakturaLogoCacheBust = ref(0)
const fakturaLogoUploading = ref(false)
const hasSiteLogo = ref(false)
const siteLogoMime = ref('')
const siteLogoCacheBust = ref(0)
const siteLogoUploading = ref(false)
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
    phone.value = body.phone ?? ''
    vhfChannel.value = body.vhf_channel ?? ''
    latitude.value = body.latitude != null ? String(body.latitude) : ''
    longitude.value = body.longitude != null ? String(body.longitude) : ''
    harborApproach.value = body.harbor_approach ?? ''
    harborDepth.value = body.harbor_depth ?? ''
    harborVhf.value = body.harbor_vhf ?? ''
    harborCtaTitle.value = body.harbor_cta_title ?? ''
    harborCtaDescription.value = body.harbor_cta_description ?? ''
    motorhomePower.value = body.motorhome_power ?? ''
    motorhomeFacilities.value = body.motorhome_facilities ?? ''
    motorhomeCheckin.value = body.motorhome_checkin ?? ''
    motorhomeRules.value = body.motorhome_rules ?? ''
    motorhomeCtaTitle.value = body.motorhome_cta_title ?? ''
    motorhomeCtaDescription.value = body.motorhome_cta_description ?? ''
    bankAccount.value = body.bank_account ?? ''
    websiteUrl.value = body.website_url ?? ''
    chairmanEmail.value = body.chairman_email ?? ''
    viceChairmanEmail.value = body.vice_chairman_email ?? ''
    treasurerEmail.value = body.treasurer_email ?? ''
    secretaryEmail.value = body.secretary_email ?? ''
    harborMasterEmail.value = body.harbor_master_email ?? ''
    hasFakturaLogo.value = !!body.has_faktura_logo
    fakturaLogoMime.value = body.faktura_logo_mime ?? ''
    fakturaLogoCacheBust.value = Date.now()
    hasSiteLogo.value = !!body.has_site_logo
    siteLogoMime.value = body.site_logo_mime ?? ''
    siteLogoCacheBust.value = Date.now()
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    loading.value = false
  }
}

onMounted(load)

// uploadLogoFile is shared between the faktura and site logo widgets.
// `kind` selects the endpoint, accepted MIME types, and which reactive
// state slots get updated on success.
async function uploadLogoFile(e: Event, kind: 'faktura' | 'site') {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  const isFaktura = kind === 'faktura'
  if (isFaktura) {
    if (file.type !== 'image/png' && file.type !== 'image/jpeg') {
      error.value = t('admin.financialSettings.logoMimeError')
      input.value = ''
      return
    }
  } else {
    // SVG is sniffed server-side; client-side we accept the browser's
    // best guess plus an empty type (some browsers omit it for SVG).
    if (file.type && file.type !== 'image/svg+xml') {
      error.value = t('admin.financialSettings.siteLogoMimeError')
      input.value = ''
      return
    }
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
  if (isFaktura) fakturaLogoUploading.value = true
  else siteLogoUploading.value = true
  error.value = null
  try {
    const fd = new FormData()
    fd.append('logo', file)
    const url = isFaktura
      ? '/api/v1/admin/settings/financials/faktura-logo'
      : '/api/v1/admin/settings/site-logo'
    const res = await fetch(url, { method: 'POST', credentials: 'include', body: fd })
    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      throw new Error(`${res.status} ${txt}`)
    }
    const body = await res.json()
    if (isFaktura) {
      hasFakturaLogo.value = true
      fakturaLogoMime.value = body.mime ?? ''
      fakturaLogoCacheBust.value = Date.now()
    } else {
      hasSiteLogo.value = true
      siteLogoMime.value = body.mime ?? ''
      siteLogoCacheBust.value = Date.now()
    }
  } catch (err) {
    error.value = (err as Error).message
  } finally {
    if (isFaktura) fakturaLogoUploading.value = false
    else siteLogoUploading.value = false
    input.value = ''
  }
}

async function uploadFakturaLogo(e: Event) { return uploadLogoFile(e, 'faktura') }
async function uploadSiteLogo(e: Event) { return uploadLogoFile(e, 'site') }

async function deleteLogoFile(kind: 'faktura' | 'site') {
  if (!confirm(t('admin.financialSettings.logoDeleteConfirm'))) return
  if (!(await ensureFreshTotp())) return
  error.value = null
  try {
    const url = kind === 'faktura'
      ? '/api/v1/admin/settings/financials/faktura-logo'
      : '/api/v1/admin/settings/site-logo'
    const res = await fetch(url, { method: 'DELETE', credentials: 'include' })
    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      throw new Error(`${res.status} ${txt}`)
    }
    if (kind === 'faktura') {
      hasFakturaLogo.value = false
      fakturaLogoMime.value = ''
      fakturaLogoCacheBust.value = Date.now()
    } else {
      hasSiteLogo.value = false
      siteLogoMime.value = ''
      siteLogoCacheBust.value = Date.now()
    }
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
        phone: phone.value,
        vhf_channel: vhfChannel.value,
        latitude: latitude.value === '' ? null : Number(latitude.value),
        longitude: longitude.value === '' ? null : Number(longitude.value),
        harbor_approach: harborApproach.value,
        harbor_depth: harborDepth.value,
        harbor_vhf: harborVhf.value,
        harbor_cta_title: harborCtaTitle.value,
        harbor_cta_description: harborCtaDescription.value,
        motorhome_power: motorhomePower.value,
        motorhome_facilities: motorhomeFacilities.value,
        motorhome_checkin: motorhomeCheckin.value,
        motorhome_rules: motorhomeRules.value,
        motorhome_cta_title: motorhomeCtaTitle.value,
        motorhome_cta_description: motorhomeCtaDescription.value,
        bank_account: bankAccount.value,
        website_url: websiteUrl.value,
        chairman_email: chairmanEmail.value,
        vice_chairman_email: viceChairmanEmail.value,
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
      <fieldset class="rounded-md border border-slate-200 bg-slate-50 p-3 space-y-3">
        <legend class="px-1 text-xs font-semibold text-slate-700">{{ t('admin.financialSettings.identityGroup') }}</legend>
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
          class="mt-1 w-full rounded-md border border-slate-300 bg-white px-2 py-1 text-sm"
          placeholder="999 999 999"
        />
      </div>
      </fieldset>

      <fieldset class="rounded-md border border-slate-200 bg-slate-50 p-3 space-y-3">
        <legend class="px-1 text-xs font-semibold text-slate-700">{{ t('admin.financialSettings.contactGroup') }}</legend>
        <div>
          <label class="block text-xs font-medium text-slate-700">{{ t('admin.financialSettings.address') }}</label>
          <textarea
            v-model="address"
            rows="2"
            class="mt-1 w-full rounded-md border border-slate-300 bg-white px-2 py-1 text-sm"
            placeholder="Brygga 1, 5378 Klokkarvik"
          />
        </div>
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div>
            <label class="block text-xs font-medium text-slate-700">{{ t('admin.financialSettings.phone') }}</label>
            <input
              v-model="phone"
              type="tel"
              class="mt-1 w-full rounded-md border border-slate-300 bg-white px-2 py-1 text-sm"
              placeholder="+47 22 00 00 00"
            />
            <p class="mt-1 text-xs text-slate-500">{{ t('admin.financialSettings.phoneHint') }}</p>
          </div>
          <div>
            <label class="block text-xs font-medium text-slate-700">{{ t('admin.financialSettings.vhfChannel') }}</label>
            <input
              v-model="vhfChannel"
              type="text"
              class="mt-1 w-full rounded-md border border-slate-300 bg-white px-2 py-1 text-sm"
              placeholder="Ch 73"
            />
            <p class="mt-1 text-xs text-slate-500">{{ t('admin.financialSettings.vhfChannelHint') }}</p>
          </div>
        </div>
      </fieldset>

      <fieldset class="rounded-md border border-slate-200 bg-slate-50 p-3 space-y-4">
        <legend class="px-1 text-xs font-semibold text-slate-700">{{ t('admin.financialSettings.logosGroup') }}</legend>
      <div>
        <label class="block text-xs font-medium text-gray-700">{{ t('admin.financialSettings.fakturaLogo') }}</label>
        <p class="mt-1 text-xs text-gray-500">{{ t('admin.financialSettings.fakturaLogoHint') }}</p>
        <div class="mt-2 flex items-center gap-3">
          <img
            v-if="hasFakturaLogo"
            :src="`/api/v1/admin/settings/financials/faktura-logo?v=${fakturaLogoCacheBust}`"
            alt="Faktura logo"
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
              :disabled="fakturaLogoUploading"
              @change="uploadFakturaLogo"
            />
            {{ fakturaLogoUploading ? t('common.loading') : (hasFakturaLogo ? t('admin.financialSettings.logoReplace') : t('admin.financialSettings.logoUpload')) }}
          </label>
          <button
            v-if="hasFakturaLogo"
            type="button"
            class="rounded-md border border-red-300 bg-white px-3 py-1.5 text-sm text-red-700 hover:bg-red-50"
            @click="deleteLogoFile('faktura')"
          >
            {{ t('common.delete') }}
          </button>
        </div>
      </div>

      <div>
        <label class="block text-xs font-medium text-gray-700">{{ t('admin.financialSettings.siteLogo') }}</label>
        <p class="mt-1 text-xs text-gray-500">{{ t('admin.financialSettings.siteLogoHint') }}</p>
        <div class="mt-2 flex items-center gap-3">
          <img
            v-if="hasSiteLogo"
            :src="`/api/v1/admin/settings/site-logo?v=${siteLogoCacheBust}`"
            alt="Site logo"
            class="h-16 rounded border border-gray-200 bg-white p-1 object-contain"
          />
          <span v-else class="text-xs italic text-gray-400">{{ t('admin.financialSettings.logoNone') }}</span>
        </div>
        <div class="mt-2 flex items-center gap-2">
          <label class="inline-flex cursor-pointer items-center gap-1 rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm text-gray-700 hover:bg-gray-50">
            <input
              type="file"
              accept="image/svg+xml,.svg"
              class="hidden"
              :disabled="siteLogoUploading"
              @change="uploadSiteLogo"
            />
            {{ siteLogoUploading ? t('common.loading') : (hasSiteLogo ? t('admin.financialSettings.logoReplace') : t('admin.financialSettings.logoUpload')) }}
          </label>
          <button
            v-if="hasSiteLogo"
            type="button"
            class="rounded-md border border-red-300 bg-white px-3 py-1.5 text-sm text-red-700 hover:bg-red-50"
            @click="deleteLogoFile('site')"
          >
            {{ t('common.delete') }}
          </button>
        </div>
      </div>

      </fieldset>

      <fieldset class="rounded-md border border-slate-200 bg-slate-50 p-3 space-y-3">
        <legend class="px-1 text-xs font-semibold text-slate-700">{{ t('admin.financialSettings.bankingGroup') }}</legend>
      <div>
        <label class="block text-xs font-medium text-slate-700">{{ t('admin.financialSettings.bankAccount') }}</label>
        <input
          v-model="bankAccount"
          type="text"
          class="mt-1 w-full rounded-md border border-slate-300 bg-white px-2 py-1 text-sm font-mono"
          placeholder="1234.56.78901"
        />
        <p class="mt-1 text-xs text-slate-500">{{ t('admin.financialSettings.bankAccountHint') }}</p>
      </div>

      <div>
        <label class="block text-xs font-medium text-slate-700">{{ t('admin.financialSettings.websiteUrl') }}</label>
        <input
          v-model="websiteUrl"
          type="url"
          class="mt-1 w-full rounded-md border border-slate-300 bg-white px-2 py-1 text-sm"
          placeholder="https://klokkarvikbaatlag.no"
        />
        <p class="mt-1 text-xs text-slate-500">{{ t('admin.financialSettings.websiteUrlHint') }}</p>
      </div>
      </fieldset>

      <fieldset class="rounded-md border border-slate-200 bg-slate-50 p-3">
        <legend class="px-1 text-xs font-semibold text-slate-700">{{ t('admin.financialSettings.boardEmails') }}</legend>
        <p class="mb-2 text-xs text-slate-500">{{ t('admin.financialSettings.boardEmailsHint') }}</p>
        <!-- Order: leder → nestleder → havnesjef → sekretær → kasserer.
             Mirrors the contact page so admins reading the two side by
             side see the same hierarchy. -->
        <div class="grid gap-3 sm:grid-cols-2">
          <div>
            <label class="block text-xs font-medium text-slate-700">{{ t('admin.financialSettings.chairmanEmail') }}</label>
            <input v-model="chairmanEmail" type="email" class="mt-1 w-full rounded-md border border-slate-300 bg-white px-2 py-1 text-sm" placeholder="leder@klubb.no" />
          </div>
          <div>
            <label class="block text-xs font-medium text-slate-700">{{ t('admin.financialSettings.viceChairmanEmail') }}</label>
            <input v-model="viceChairmanEmail" type="email" class="mt-1 w-full rounded-md border border-slate-300 bg-white px-2 py-1 text-sm" placeholder="nestleder@klubb.no" />
          </div>
          <div>
            <label class="block text-xs font-medium text-slate-700">{{ t('admin.financialSettings.harborMasterEmail') }}</label>
            <input v-model="harborMasterEmail" type="email" class="mt-1 w-full rounded-md border border-slate-300 bg-white px-2 py-1 text-sm" placeholder="havnesjef@klubb.no" />
          </div>
          <div>
            <label class="block text-xs font-medium text-slate-700">{{ t('admin.financialSettings.secretaryEmail') }}</label>
            <input v-model="secretaryEmail" type="email" class="mt-1 w-full rounded-md border border-slate-300 bg-white px-2 py-1 text-sm" placeholder="sekretaer@klubb.no" />
          </div>
          <div>
            <label class="block text-xs font-medium text-slate-700">{{ t('admin.financialSettings.treasurerEmail') }}</label>
            <input v-model="treasurerEmail" type="email" class="mt-1 w-full rounded-md border border-slate-300 bg-white px-2 py-1 text-sm" placeholder="kasserer@klubb.no" />
            <p class="mt-1 text-xs text-slate-500">{{ t('admin.financialSettings.treasurerEmailHint') }}</p>
          </div>
        </div>
      </fieldset>

      <fieldset class="rounded-md border border-slate-200 bg-slate-50 p-3">
        <legend class="px-1 text-xs font-semibold text-gray-700">{{ t('admin.financialSettings.coordinates') }}</legend>
        <p class="mb-2 text-xs text-gray-500">{{ t('admin.financialSettings.coordinatesHint') }}</p>
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div>
            <label class="block text-xs font-medium text-gray-700">{{ t('admin.financialSettings.latitude') }}</label>
            <input
              v-model="latitude"
              type="number"
              step="0.000001"
              class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm font-mono"
              placeholder="60.2334"
            />
          </div>
          <div>
            <label class="block text-xs font-medium text-gray-700">{{ t('admin.financialSettings.longitude') }}</label>
            <input
              v-model="longitude"
              type="number"
              step="0.000001"
              class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm font-mono"
              placeholder="5.1245"
            />
          </div>
        </div>
      </fieldset>

      <fieldset class="rounded-md border border-slate-200 bg-slate-50 p-3">
        <legend class="px-1 text-xs font-semibold text-gray-700">{{ t('admin.financialSettings.harborContent') }}</legend>
        <p class="mb-2 text-xs text-gray-500">{{ t('admin.financialSettings.harborContentHint') }}</p>
        <div class="space-y-3">
          <div>
            <label class="block text-xs font-medium text-gray-700">{{ t('admin.financialSettings.harborApproach') }}</label>
            <textarea v-model="harborApproach" rows="2" class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm" />
          </div>
          <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div>
              <label class="block text-xs font-medium text-gray-700">{{ t('admin.financialSettings.harborDepth') }}</label>
              <input v-model="harborDepth" type="text" class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm" />
            </div>
            <div>
              <label class="block text-xs font-medium text-gray-700">{{ t('admin.financialSettings.harborVhf') }}</label>
              <input v-model="harborVhf" type="text" class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm" placeholder="Ch 16 / Ch 73" />
            </div>
          </div>
          <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div>
              <label class="block text-xs font-medium text-gray-700">{{ t('admin.financialSettings.ctaTitle') }}</label>
              <input v-model="harborCtaTitle" type="text" class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm" />
            </div>
            <div>
              <label class="block text-xs font-medium text-gray-700">{{ t('admin.financialSettings.ctaDescription') }}</label>
              <input v-model="harborCtaDescription" type="text" class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm" />
            </div>
          </div>
        </div>
      </fieldset>

      <fieldset class="rounded-md border border-slate-200 bg-slate-50 p-3">
        <legend class="px-1 text-xs font-semibold text-gray-700">{{ t('admin.financialSettings.motorhomeContent') }}</legend>
        <p class="mb-2 text-xs text-gray-500">{{ t('admin.financialSettings.motorhomeContentHint') }}</p>
        <div class="space-y-3">
          <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div>
              <label class="block text-xs font-medium text-gray-700">{{ t('admin.financialSettings.motorhomePower') }}</label>
              <textarea v-model="motorhomePower" rows="2" class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm" />
            </div>
            <div>
              <label class="block text-xs font-medium text-gray-700">{{ t('admin.financialSettings.motorhomeFacilities') }}</label>
              <textarea v-model="motorhomeFacilities" rows="2" class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm" />
            </div>
            <div>
              <label class="block text-xs font-medium text-gray-700">{{ t('admin.financialSettings.motorhomeCheckin') }}</label>
              <textarea v-model="motorhomeCheckin" rows="2" class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm" />
            </div>
            <div>
              <label class="block text-xs font-medium text-gray-700">{{ t('admin.financialSettings.motorhomeRules') }}</label>
              <textarea v-model="motorhomeRules" rows="2" class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm" />
            </div>
          </div>
          <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div>
              <label class="block text-xs font-medium text-gray-700">{{ t('admin.financialSettings.ctaTitle') }}</label>
              <input v-model="motorhomeCtaTitle" type="text" class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm" />
            </div>
            <div>
              <label class="block text-xs font-medium text-gray-700">{{ t('admin.financialSettings.ctaDescription') }}</label>
              <input v-model="motorhomeCtaDescription" type="text" class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm" />
            </div>
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
