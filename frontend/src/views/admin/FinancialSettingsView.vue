<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ArrowLeft, Save } from 'lucide-vue-next'
import { useTotpGateStore } from '@/stores/totpGate'
import { useAuthStore } from '@/stores/auth'
import FileInput from '@/components/ui/form/FileInput.vue'
import FormField from '@/components/ui/form/FormField.vue'
import Input from '@/components/ui/form/Input.vue'
import NumberInput from '@/components/ui/form/NumberInput.vue'
import Textarea from '@/components/ui/form/Textarea.vue'

const { t } = useI18n()
const auth = useAuthStore()
const totpGate = useTotpGateStore()

const clubName = ref('')
const orgNumber = ref('')
const address = ref('')
const phone = ref('')
const vhfChannel = ref('')
const latitude = ref<number | null>(null)
const longitude = ref<number | null>(null)

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
    latitude.value = body.latitude != null ? Number(body.latitude) : null
    longitude.value = body.longitude != null ? Number(body.longitude) : null
    motorhomePower.value = body.motorhome_power ?? ''
    motorhomeFacilities.value = body.motorhome_facilities ?? ''
    motorhomeCheckin.value = body.motorhome_checkin ?? ''
    motorhomeRules.value = body.motorhome_rules ?? ''
    motorhomeCtaTitle.value = body.motorhome_cta_title ?? ''
    motorhomeCtaDescription.value = body.motorhome_cta_description ?? ''
    websiteUrl.value = body.website_url ?? ''
    chairmanEmail.value = body.chairman_email ?? ''
    viceChairmanEmail.value = body.vice_chairman_email ?? ''
    treasurerEmail.value = body.treasurer_email ?? ''
    secretaryEmail.value = body.secretary_email ?? ''
    harborMasterEmail.value = body.harbor_master_email ?? ''
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

async function uploadSiteLogo(files: FileList | null) {
  const file = files?.[0]
  if (!file) return
  if (file.type && file.type !== 'image/svg+xml') {
    error.value = t('admin.financialSettings.siteLogoMimeError')
    return
  }
  if (file.size > 2 * 1024 * 1024) {
    error.value = t('admin.financialSettings.logoSizeError')
    return
  }
  if (!(await ensureFreshTotp())) return
  siteLogoUploading.value = true
  error.value = null
  try {
    const fd = new FormData()
    fd.append('logo', file)
    const res = await fetch('/api/v1/admin/settings/site-logo', {
      method: 'POST',
      credentials: 'include',
      body: fd,
    })
    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      throw new Error(`${res.status} ${txt}`)
    }
    const body = await res.json()
    hasSiteLogo.value = true
    siteLogoMime.value = body.mime ?? ''
    siteLogoCacheBust.value = Date.now()
  } catch (err) {
    error.value = (err as Error).message
  } finally {
    siteLogoUploading.value = false
  }
}

async function deleteSiteLogo() {
  if (!confirm(t('admin.financialSettings.logoDeleteConfirm'))) return
  if (!(await ensureFreshTotp())) return
  error.value = null
  try {
    const res = await fetch('/api/v1/admin/settings/site-logo', {
      method: 'DELETE',
      credentials: 'include',
    })
    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      throw new Error(`${res.status} ${txt}`)
    }
    hasSiteLogo.value = false
    siteLogoMime.value = ''
    siteLogoCacheBust.value = Date.now()
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
        latitude: latitude.value,
        longitude: longitude.value,
        motorhome_power: motorhomePower.value,
        motorhome_facilities: motorhomeFacilities.value,
        motorhome_checkin: motorhomeCheckin.value,
        motorhome_rules: motorhomeRules.value,
        motorhome_cta_title: motorhomeCtaTitle.value,
        motorhome_cta_description: motorhomeCtaDescription.value,
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
      <FormField :label="t('admin.financialSettings.clubName')" :helper-text="t('admin.financialSettings.clubNameHint')">
        <Input :model-value="clubName" disabled />
      </FormField>

      <FormField :label="t('admin.financialSettings.orgNumber')">
        <Input v-model="orgNumber" placeholder="999 999 999" />
      </FormField>
      </fieldset>

      <fieldset class="rounded-md border border-slate-200 bg-slate-50 p-3 space-y-3">
        <legend class="px-1 text-xs font-semibold text-slate-700">{{ t('admin.financialSettings.contactGroup') }}</legend>
        <FormField :label="t('admin.financialSettings.address')">
          <Textarea v-model="address" :rows="2" placeholder="Brygga 1, 5378 Klokkarvik" />
        </FormField>
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <FormField :label="t('admin.financialSettings.phone')" :helper-text="t('admin.financialSettings.phoneHint')">
            <Input v-model="phone" type="tel" placeholder="+47 22 00 00 00" />
          </FormField>
          <FormField :label="t('admin.financialSettings.vhfChannel')" :helper-text="t('admin.financialSettings.vhfChannelHint')">
            <Input v-model="vhfChannel" placeholder="Ch 73" />
          </FormField>
        </div>
      </fieldset>

      <fieldset class="rounded-md border border-slate-200 bg-slate-50 p-3 space-y-4">
        <legend class="px-1 text-xs font-semibold text-slate-700">{{ t('admin.financialSettings.logosGroup') }}</legend>
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
          <FileInput
            accept="image/svg+xml,.svg"
            :dropzone="false"
            :disabled="siteLogoUploading"
            @change="uploadSiteLogo"
          >
            <template #trigger="{ open }">
              <button
                type="button"
                class="inline-flex cursor-pointer items-center gap-1 rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm text-gray-700 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-60"
                :disabled="siteLogoUploading"
                @click="open"
              >
                {{ siteLogoUploading ? t('common.loading') : (hasSiteLogo ? t('admin.financialSettings.logoReplace') : t('admin.financialSettings.logoUpload')) }}
              </button>
            </template>
          </FileInput>
          <button
            v-if="hasSiteLogo"
            type="button"
            class="rounded-md border border-red-300 bg-white px-3 py-1.5 text-sm text-red-700 hover:bg-red-50"
            @click="deleteSiteLogo"
          >
            {{ t('common.delete') }}
          </button>
        </div>
      </div>

      </fieldset>

      <fieldset class="rounded-md border border-slate-200 bg-slate-50 p-3 space-y-3">
        <legend class="px-1 text-xs font-semibold text-slate-700">{{ t('admin.financialSettings.websiteUrl') }}</legend>
        <FormField :label="t('admin.financialSettings.websiteUrl')" :helper-text="t('admin.financialSettings.websiteUrlHint')">
          <Input v-model="websiteUrl" type="url" placeholder="https://klokkarvikbaatlag.no" />
        </FormField>
      </fieldset>

      <fieldset class="rounded-md border border-slate-200 bg-slate-50 p-3">
        <legend class="px-1 text-xs font-semibold text-slate-700">{{ t('admin.financialSettings.boardEmails') }}</legend>
        <p class="mb-2 text-xs text-slate-500">{{ t('admin.financialSettings.boardEmailsHint') }}</p>
        <!-- Order: leder → nestleder → havnesjef → sekretær → kasserer.
             Mirrors the contact page so admins reading the two side by
             side see the same hierarchy. -->
        <div class="grid gap-3 sm:grid-cols-2">
          <FormField :label="t('admin.financialSettings.chairmanEmail')">
            <Input v-model="chairmanEmail" type="email" placeholder="leder@klubb.no" />
          </FormField>
          <FormField :label="t('admin.financialSettings.viceChairmanEmail')">
            <Input v-model="viceChairmanEmail" type="email" placeholder="nestleder@klubb.no" />
          </FormField>
          <FormField :label="t('admin.financialSettings.harborMasterEmail')">
            <Input v-model="harborMasterEmail" type="email" placeholder="havnesjef@klubb.no" />
          </FormField>
          <FormField :label="t('admin.financialSettings.secretaryEmail')">
            <Input v-model="secretaryEmail" type="email" placeholder="sekretaer@klubb.no" />
          </FormField>
          <FormField :label="t('admin.financialSettings.treasurerEmail')" :helper-text="t('admin.financialSettings.treasurerEmailHint')">
            <Input v-model="treasurerEmail" type="email" placeholder="kasserer@klubb.no" />
          </FormField>
        </div>
      </fieldset>

      <fieldset class="rounded-md border border-slate-200 bg-slate-50 p-3">
        <legend class="px-1 text-xs font-semibold text-gray-700">{{ t('admin.financialSettings.coordinates') }}</legend>
        <p class="mb-2 text-xs text-gray-500">{{ t('admin.financialSettings.coordinatesHint') }}</p>
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <FormField :label="t('admin.financialSettings.latitude')">
            <NumberInput v-model="latitude" :step="0.000001" placeholder="60.2334" />
          </FormField>
          <FormField :label="t('admin.financialSettings.longitude')">
            <NumberInput v-model="longitude" :step="0.000001" placeholder="5.1245" />
          </FormField>
        </div>
      </fieldset>

      <fieldset class="rounded-md border border-slate-200 bg-slate-50 p-3">
        <legend class="px-1 text-xs font-semibold text-gray-700">{{ t('admin.financialSettings.motorhomeContent') }}</legend>
        <p class="mb-2 text-xs text-gray-500">{{ t('admin.financialSettings.motorhomeContentHint') }}</p>
        <div class="space-y-3">
          <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <FormField :label="t('admin.financialSettings.motorhomePower')">
              <Textarea v-model="motorhomePower" :rows="2" />
            </FormField>
            <FormField :label="t('admin.financialSettings.motorhomeFacilities')">
              <Textarea v-model="motorhomeFacilities" :rows="2" />
            </FormField>
            <FormField :label="t('admin.financialSettings.motorhomeCheckin')">
              <Textarea v-model="motorhomeCheckin" :rows="2" />
            </FormField>
            <FormField :label="t('admin.financialSettings.motorhomeRules')">
              <Textarea v-model="motorhomeRules" :rows="2" />
            </FormField>
          </div>
          <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <FormField :label="t('admin.financialSettings.ctaTitle')">
              <Input v-model="motorhomeCtaTitle" />
            </FormField>
            <FormField :label="t('admin.financialSettings.ctaDescription')">
              <Input v-model="motorhomeCtaDescription" />
            </FormField>
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
