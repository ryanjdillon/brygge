<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useFreshTotp } from '@/composables/useFreshTotp'
import FileInput from '@/components/ui/form/FileInput.vue'
import Tabs from '@/components/ui/Tabs.vue'
import BankAccountsPanel from '@/components/admin/BankAccountsPanel.vue'

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

async function load() {
  loading.value = true
  error.value = null
  try {
    const res = await fetch('/api/v1/admin/settings/site', { credentials: 'include' })
    if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
    const body = await res.json()
    hasFakturaLogo.value = !!body.has_faktura_logo
    kidCheckMethod.value = body.kid_check_method === 'mod10' ? 'mod10' : 'mod11'
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
    </section>

    <BankAccountsPanel v-else-if="activeTab === 'bank-accounts'" />
  </div>
</template>
