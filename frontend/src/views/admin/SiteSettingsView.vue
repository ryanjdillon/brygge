<script setup lang="ts">
import { reactive, ref, onMounted } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useQueryClient } from '@tanstack/vue-query'
import { ArrowLeft, Calculator, Calendar, CalendarCheck, FolderKanban, Megaphone, Save, ShoppingBag } from 'lucide-vue-next'
import { useTotpGateStore } from '@/stores/totpGate'
import { useAuthStore } from '@/stores/auth'
import FileInput from '@/components/ui/form/FileInput.vue'
import FormField from '@/components/ui/form/FormField.vue'
import Input from '@/components/ui/form/Input.vue'
import NumberInput from '@/components/ui/form/NumberInput.vue'
import Switch from '@/components/ui/form/Switch.vue'
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

const queryClient = useQueryClient()

const features = reactive({
  bookings: true,
  projects: true,
  calendar: true,
  commerce: true,
  communications: true,
  accounting: true,
})

type ModuleKey = keyof typeof features
const moduleRows: { key: ModuleKey; icon: typeof Calculator; descriptionKey?: string }[] = [
  { key: 'bookings', icon: CalendarCheck },
  { key: 'projects', icon: FolderKanban },
  { key: 'calendar', icon: Calendar },
  { key: 'commerce', icon: ShoppingBag },
  { key: 'communications', icon: Megaphone },
  { key: 'accounting', icon: Calculator },
]

async function ensureFreshTotp(): Promise<boolean> {
  if (auth.hasFreshTotp) return true
  return totpGate.open()
}

async function load() {
  loading.value = true
  error.value = null
  try {
    const res = await fetch('/api/v1/admin/settings/site', { credentials: 'include' })
    if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
    const body = await res.json()
    clubName.value = body.name ?? ''
    orgNumber.value = body.org_number ?? ''
    address.value = body.address ?? ''
    phone.value = body.phone ?? ''
    vhfChannel.value = body.vhf_channel ?? ''
    latitude.value = body.latitude != null ? Number(body.latitude) : null
    longitude.value = body.longitude != null ? Number(body.longitude) : null
    websiteUrl.value = body.website_url ?? ''
    chairmanEmail.value = body.chairman_email ?? ''
    viceChairmanEmail.value = body.vice_chairman_email ?? ''
    treasurerEmail.value = body.treasurer_email ?? ''
    secretaryEmail.value = body.secretary_email ?? ''
    harborMasterEmail.value = body.harbor_master_email ?? ''
    hasSiteLogo.value = !!body.has_site_logo
    siteLogoMime.value = body.site_logo_mime ?? ''
    siteLogoCacheBust.value = Date.now()
    features.bookings = body.feature_bookings ?? true
    features.projects = body.feature_projects ?? true
    features.calendar = body.feature_calendar ?? true
    features.commerce = body.feature_commerce ?? true
    features.communications = body.feature_communications ?? true
    features.accounting = body.feature_accounting ?? true
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
    error.value = t('admin.siteSettings.siteLogoMimeError')
    return
  }
  if (file.size > 2 * 1024 * 1024) {
    error.value = t('admin.siteSettings.logoSizeError')
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
  if (!confirm(t('admin.siteSettings.logoDeleteConfirm'))) return
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
    const res = await fetch('/api/v1/admin/settings/site', {
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
        website_url: websiteUrl.value,
        chairman_email: chairmanEmail.value,
        vice_chairman_email: viceChairmanEmail.value,
        treasurer_email: treasurerEmail.value,
        secretary_email: secretaryEmail.value,
        harbor_master_email: harborMasterEmail.value,
        feature_bookings: features.bookings,
        feature_projects: features.projects,
        feature_calendar: features.calendar,
        feature_commerce: features.commerce,
        feature_communications: features.communications,
        feature_accounting: features.accounting,
      }),
    })
    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      throw new Error(`${res.status} ${txt}`)
    }
    savedAt.value = new Date()
    // Refresh the public features cache so the sidebar updates
    // without a full reload after a module is toggled.
    queryClient.invalidateQueries({ queryKey: ['features'] })
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

    <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.siteSettings.title') }}</h1>
    <p class="mt-1 text-sm text-gray-600">{{ t('admin.siteSettings.subtitle') }}</p>

    <p v-if="loading" class="mt-6 text-sm text-gray-500">{{ t('common.loading') }}…</p>

    <form v-else class="mt-6 max-w-xl space-y-4" @submit.prevent="save">
      <section class="rounded-lg border border-slate-200 bg-white">
        <header class="border-b border-slate-100 px-4 py-3">
          <h3 class="text-sm font-semibold text-slate-900">{{ t('admin.siteSettings.modulesGroup') }}</h3>
          <p class="mt-1 text-xs text-slate-500">{{ t('admin.siteSettings.modulesHint') }}</p>
        </header>
        <ul class="divide-y divide-slate-100">
          <li v-for="m in moduleRows" :key="m.key" class="flex items-center gap-3 px-4 py-3">
            <component :is="m.icon" class="h-5 w-5 shrink-0 text-slate-500" />
            <div class="flex-1 min-w-0">
              <p class="text-sm font-medium text-slate-900">{{ t(`admin.siteSettings.modules.${m.key}`) }}</p>
              <p v-if="m.descriptionKey" class="text-xs text-slate-500">{{ t(m.descriptionKey) }}</p>
            </div>
            <Switch :model-value="features[m.key]" @update:model-value="features[m.key] = $event" :aria-label="t(`admin.siteSettings.modules.${m.key}`)" />
          </li>
        </ul>
      </section>

      <fieldset class="rounded-md border border-slate-200 bg-slate-50 p-3 space-y-3">
        <legend class="px-1 text-xs font-semibold text-slate-700">{{ t('admin.siteSettings.identityGroup') }}</legend>
      <FormField :label="t('admin.siteSettings.clubName')" :helper-text="t('admin.siteSettings.clubNameHint')">
        <Input :model-value="clubName" disabled />
      </FormField>

      <FormField :label="t('admin.siteSettings.orgNumber')">
        <Input v-model="orgNumber" placeholder="999 999 999" />
      </FormField>
      </fieldset>

      <fieldset class="rounded-md border border-slate-200 bg-slate-50 p-3 space-y-3">
        <legend class="px-1 text-xs font-semibold text-slate-700">{{ t('admin.siteSettings.contactGroup') }}</legend>
        <FormField :label="t('admin.siteSettings.address')">
          <Textarea v-model="address" :rows="2" placeholder="Brygga 1, 5378 Klokkarvik" />
        </FormField>
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <FormField :label="t('admin.siteSettings.phone')" :helper-text="t('admin.siteSettings.phoneHint')">
            <Input v-model="phone" type="tel" placeholder="+47 22 00 00 00" />
          </FormField>
          <FormField :label="t('admin.siteSettings.vhfChannel')" :helper-text="t('admin.siteSettings.vhfChannelHint')">
            <Input v-model="vhfChannel" placeholder="Ch 73" />
          </FormField>
        </div>
      </fieldset>

      <fieldset class="rounded-md border border-slate-200 bg-slate-50 p-3 space-y-4">
        <legend class="px-1 text-xs font-semibold text-slate-700">{{ t('admin.siteSettings.logosGroup') }}</legend>
      <div>
        <label class="block text-xs font-medium text-gray-700">{{ t('admin.siteSettings.siteLogo') }}</label>
        <p class="mt-1 text-xs text-gray-500">{{ t('admin.siteSettings.siteLogoHint') }}</p>
        <div class="mt-2 flex items-center gap-3">
          <img
            v-if="hasSiteLogo"
            :src="`/api/v1/admin/settings/site-logo?v=${siteLogoCacheBust}`"
            alt="Site logo"
            class="h-16 rounded border border-gray-200 bg-white p-1 object-contain"
          />
          <span v-else class="text-xs italic text-gray-400">{{ t('admin.siteSettings.logoNone') }}</span>
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
                {{ siteLogoUploading ? t('common.loading') : (hasSiteLogo ? t('admin.siteSettings.logoReplace') : t('admin.siteSettings.logoUpload')) }}
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
        <legend class="px-1 text-xs font-semibold text-slate-700">{{ t('admin.siteSettings.websiteUrl') }}</legend>
        <FormField :label="t('admin.siteSettings.websiteUrl')" :helper-text="t('admin.siteSettings.websiteUrlHint')">
          <Input v-model="websiteUrl" type="url" placeholder="https://klokkarvikbaatlag.no" />
        </FormField>
      </fieldset>

      <fieldset class="rounded-md border border-slate-200 bg-slate-50 p-3">
        <legend class="px-1 text-xs font-semibold text-slate-700">{{ t('admin.siteSettings.boardEmails') }}</legend>
        <p class="mb-2 text-xs text-slate-500">{{ t('admin.siteSettings.boardEmailsHint') }}</p>
        <!-- Order: leder → nestleder → havnesjef → sekretær → kasserer.
             Mirrors the contact page so admins reading the two side by
             side see the same hierarchy. -->
        <div class="grid gap-3 sm:grid-cols-2">
          <FormField :label="t('admin.siteSettings.chairmanEmail')">
            <Input v-model="chairmanEmail" type="email" placeholder="leder@klubb.no" />
          </FormField>
          <FormField :label="t('admin.siteSettings.viceChairmanEmail')">
            <Input v-model="viceChairmanEmail" type="email" placeholder="nestleder@klubb.no" />
          </FormField>
          <FormField :label="t('admin.siteSettings.harborMasterEmail')">
            <Input v-model="harborMasterEmail" type="email" placeholder="havnesjef@klubb.no" />
          </FormField>
          <FormField :label="t('admin.siteSettings.secretaryEmail')">
            <Input v-model="secretaryEmail" type="email" placeholder="sekretaer@klubb.no" />
          </FormField>
          <FormField :label="t('admin.siteSettings.treasurerEmail')" :helper-text="t('admin.siteSettings.treasurerEmailHint')">
            <Input v-model="treasurerEmail" type="email" placeholder="kasserer@klubb.no" />
          </FormField>
        </div>
      </fieldset>

      <fieldset class="rounded-md border border-slate-200 bg-slate-50 p-3">
        <legend class="px-1 text-xs font-semibold text-gray-700">{{ t('admin.siteSettings.coordinates') }}</legend>
        <p class="mb-2 text-xs text-gray-500">{{ t('admin.siteSettings.coordinatesHint') }}</p>
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <FormField :label="t('admin.siteSettings.latitude')">
            <NumberInput v-model="latitude" :step="0.000001" placeholder="60.2334" />
          </FormField>
          <FormField :label="t('admin.siteSettings.longitude')">
            <NumberInput v-model="longitude" :step="0.000001" placeholder="5.1245" />
          </FormField>
        </div>
      </fieldset>

      <p v-if="error" class="rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">{{ error }}</p>
      <p v-else-if="savedAt" class="rounded-md bg-green-50 px-3 py-2 text-sm text-green-700">
        {{ t('admin.siteSettings.saved') }} ({{ savedAt.toLocaleTimeString() }})
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
