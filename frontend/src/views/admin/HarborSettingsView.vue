<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Save } from 'lucide-vue-next'
import { useTotpGateStore } from '@/stores/totpGate'
import { useAuthStore } from '@/stores/auth'
import FormField from '@/components/ui/form/FormField.vue'
import Input from '@/components/ui/form/Input.vue'
import Textarea from '@/components/ui/form/Textarea.vue'

const { t } = useI18n()
const auth = useAuthStore()
const totpGate = useTotpGateStore()

const harborApproach = ref('')
const harborDepth = ref('')
const harborVhf = ref('')
const harborCtaTitle = ref('')
const harborCtaDescription = ref('')

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
    const res = await fetch('/api/v1/admin/settings/site', { credentials: 'include' })
    if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
    const body = await res.json()
    harborApproach.value = body.harbor_approach ?? ''
    harborDepth.value = body.harbor_depth ?? ''
    harborVhf.value = body.harbor_vhf ?? ''
    harborCtaTitle.value = body.harbor_cta_title ?? ''
    harborCtaDescription.value = body.harbor_cta_description ?? ''
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    loading.value = false
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
        harbor_approach: harborApproach.value,
        harbor_depth: harborDepth.value,
        harbor_vhf: harborVhf.value,
        harbor_cta_title: harborCtaTitle.value,
        harbor_cta_description: harborCtaDescription.value,
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

onMounted(load)
</script>

<template>
  <div class="space-y-6">
    <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.harborSettings.title') }}</h1>
    <p class="text-sm text-gray-600">{{ t('admin.harborSettings.subtitle') }}</p>

    <form class="space-y-5" @submit.prevent="save">
      <FormField :label="t('admin.financialSettings.harborApproach')">
        <Textarea v-model="harborApproach" :rows="3" :disabled="loading" />
      </FormField>

      <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <FormField :label="t('admin.financialSettings.harborDepth')">
          <Input v-model="harborDepth" :disabled="loading" />
        </FormField>
        <FormField :label="t('admin.financialSettings.harborVhf')">
          <Input v-model="harborVhf" placeholder="Ch 16 / Ch 73" :disabled="loading" />
        </FormField>
      </div>

      <fieldset class="rounded-md border border-slate-200 bg-slate-50 p-3 space-y-3">
        <legend class="px-1 text-xs font-semibold text-slate-700">
          {{ t('admin.harborSettings.ctaGroup') }}
        </legend>
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <FormField :label="t('admin.financialSettings.ctaTitle')">
            <Input v-model="harborCtaTitle" :disabled="loading" />
          </FormField>
          <FormField :label="t('admin.financialSettings.ctaDescription')">
            <Input v-model="harborCtaDescription" :disabled="loading" />
          </FormField>
        </div>
      </fieldset>

      <p v-if="error" class="rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">{{ error }}</p>
      <p v-else-if="savedAt" class="rounded-md bg-green-50 px-3 py-2 text-sm text-green-700">
        {{ t('admin.financialSettings.saved') }} ({{ savedAt.toLocaleTimeString() }})
      </p>

      <div class="flex justify-end pt-2">
        <button
          type="submit"
          :disabled="saving || loading"
          class="inline-flex items-center gap-1 rounded-md bg-blue-600 px-3 py-1.5 text-sm font-semibold text-white hover:bg-blue-700 disabled:opacity-50"
        >
          <Save class="h-4 w-4" />
          {{ saving ? t('common.loading') : t('common.save') }}
        </button>
      </div>
    </form>
  </div>
</template>
