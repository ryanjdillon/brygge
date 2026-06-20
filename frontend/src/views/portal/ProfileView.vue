<script setup lang="ts">
import { ref, watch, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { RouterLink, useRoute } from 'vue-router'
import { useApiClient, unwrap } from '@/lib/apiClient'
import { useLegalDocument, useMyConsents, useRecordConsent } from '@/composables/useGdpr'
import Input from '@/components/ui/form/Input.vue'
import Checkbox from '@/components/ui/form/Checkbox.vue'
import FormField from '@/components/ui/form/FormField.vue'
import { LOCALE_OPTIONS, setLocale } from '@/i18n'
import { useAuthStore } from '@/stores/auth'

const { t } = useI18n()
const auth = useAuthStore()
const route = useRoute()
const localeOptions = LOCALE_OPTIONS
const client = useApiClient()
const queryClient = useQueryClient()

interface ProfileForm {
  name: string
  email: string
  phone: string
  address: { street: string; postalCode: string; city: string }
  isLocal: boolean
  hideInDirectory: boolean
  language: string
}

const form = ref<ProfileForm>({
  name: '',
  email: '',
  phone: '',
  address: { street: '', postalCode: '', city: '' },
  isLocal: false,
  hideInDirectory: false,
  language: '',
})

const toast = ref<{ type: 'success' | 'error'; message: string } | null>(null)
const privacyAgreed = ref(false)

const { data: privacyDoc } = useLegalDocument('privacy')
const { consents } = useMyConsents()
const { mutateAsync: recordConsent } = useRecordConsent()

const privacyConsentNeeded = computed(() => {
  const v = privacyDoc.value?.version
  if (!v) return false
  return !consents.value.some(
    (c) => c.consent_type === 'privacy' && c.version === v,
  )
})

const canSubmit = computed(() => !privacyConsentNeeded.value || privacyAgreed.value)

const { data: profile, isLoading } = useQuery({
  queryKey: ['portal', 'profile'],
  queryFn: async () => unwrap(await client.GET('/api/v1/members/me')),
})

watch(profile, (p) => {
  if (p) {
    form.value = {
      name: p.full_name,
      email: p.email,
      phone: p.phone,
      address: { street: p.address_line, postalCode: p.postal_code, city: p.city },
      isLocal: p.is_local,
      hideInDirectory: p.hide_in_directory ?? false,
      language: (p as { preferred_language?: string | null }).preferred_language ?? '',
    }
  }
}, { immediate: true })

const { mutate: saveProfile, isPending: isSaving } = useMutation({
  mutationFn: async () => {
    if (privacyConsentNeeded.value && privacyDoc.value?.version) {
      await recordConsent({ consent_type: 'privacy', version: privacyDoc.value.version })
    }
    return unwrap(await client.PUT('/api/v1/members/me', {
      body: {
        full_name: form.value.name,
        phone: form.value.phone,
        address_line: form.value.address.street,
        postal_code: form.value.address.postalCode,
        city: form.value.address.city,
        hide_in_directory: form.value.hideInDirectory,
        preferred_language: form.value.language,
      } as any,
    }))
  },
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['portal', 'profile'] })
    // Apply immediately. A chosen language is the durable explicit
    // preference (persist); clearing it reverts to the club default
    // (don't persist so it keeps tracking the club).
    if (form.value.language) {
      setLocale(form.value.language, { persist: true })
    } else {
      setLocale(auth.user?.clubDefaultLanguage ?? 'nb')
    }
    toast.value = { type: 'success', message: t('portal.profile.saveSuccess') }
    setTimeout(() => (toast.value = null), 3000)
  },
  onError: () => {
    toast.value = { type: 'error', message: t('portal.profile.saveError') }
    setTimeout(() => (toast.value = null), 3000)
  },
})

interface EmailPref {
  category: string
  email_enabled: boolean
  can_opt_out: boolean
}

const { data: emailPrefs } = useQuery<EmailPref[]>({
  queryKey: ['portal', 'email-preferences'],
  queryFn: async () => {
    const res = await fetch('/api/v1/members/me/email-preferences', { credentials: 'include' })
    if (!res.ok) throw new Error(`${res.status}`)
    return res.json() as Promise<EmailPref[]>
  },
})

const { mutate: updateEmailPref } = useMutation({
  mutationFn: async (payload: { category: string; email_enabled: boolean }) => {
    const res = await fetch('/api/v1/members/me/email-preferences', {
      method: 'PUT',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    })
    if (!res.ok) throw new Error(`${res.status}`)
    return res.json()
  },
  onSuccess: () => queryClient.invalidateQueries({ queryKey: ['portal', 'email-preferences'] }),
})

const emailPrefsRef = ref<HTMLElement | null>(null)
onMounted(() => {
  if (route.query.unsubscribe) {
    emailPrefsRef.value?.scrollIntoView({ behavior: 'smooth' })
  }
})
</script>

<template>
  <div>
    <h1 class="text-2xl font-bold text-gray-900">{{ t('portal.profile.title') }}</h1>

    <div
      v-if="toast"
      :class="[
        'mt-4 rounded-md p-3 text-sm',
        toast.type === 'success' ? 'bg-green-50 text-green-800' : 'bg-red-50 text-red-800',
      ]"
    >
      {{ toast.message }}
    </div>

    <div v-if="isLoading" class="mt-6 text-gray-500">{{ t('common.loading') }}...</div>

    <form v-else class="mt-6 max-w-lg space-y-5" @submit.prevent="saveProfile()">
      <FormField :label="t('portal.profile.fullName')" for="profile-name" required>
        <Input id="profile-name" v-model="form.name" type="text" required />
      </FormField>

      <FormField :label="t('portal.profile.email')" for="profile-email" required>
        <Input id="profile-email" v-model="form.email" type="email" required />
      </FormField>

      <FormField :label="t('portal.profile.phone')" for="profile-phone">
        <Input id="profile-phone" v-model="form.phone" type="tel" />
      </FormField>

      <fieldset>
        <legend class="text-sm font-medium text-gray-700">{{ t('portal.profile.address') }}</legend>
        <div class="mt-2 space-y-3">
          <Input
            v-model="form.address.street"
            type="text"
            :aria-label="t('portal.profile.street')"
            :placeholder="t('portal.profile.street')"
          />
          <div class="grid grid-cols-2 gap-3">
            <Input
              v-model="form.address.postalCode"
              type="text"
              :aria-label="t('portal.profile.postalCode')"
              :placeholder="t('portal.profile.postalCode')"
            />
            <Input
              v-model="form.address.city"
              type="text"
              :aria-label="t('portal.profile.city')"
              :placeholder="t('portal.profile.city')"
            />
          </div>
        </div>
      </fieldset>

      <div>
        <p class="text-sm font-medium text-gray-700">{{ t('portal.profile.localStatus') }}</p>
        <span
          :class="[
            'mt-1 inline-block rounded-full px-3 py-1 text-xs font-medium',
            form.isLocal ? 'bg-green-100 text-green-800' : 'bg-yellow-100 text-yellow-800',
          ]"
        >
          {{ form.isLocal ? t('portal.profile.local') : t('portal.profile.nonLocal') }}
        </span>
      </div>

      <FormField :label="t('portal.profile.language')" for="profile-language">
        <select
          id="profile-language"
          v-model="form.language"
          class="w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        >
          <option value="">{{ t('portal.profile.languageDefault') }}</option>
          <option v-for="opt in localeOptions" :key="opt.code" :value="opt.code">
            {{ opt.label }}
          </option>
        </select>
        <p class="mt-1 text-xs text-gray-500">{{ t('portal.profile.languageHelp') }}</p>
      </FormField>

      <fieldset class="rounded-md border border-gray-200 bg-gray-50 p-3">
        <legend class="px-1 text-sm font-semibold text-gray-700">{{ t('portal.profile.privacyTitle') }}</legend>
        <Checkbox v-model="form.hideInDirectory">
          <span>
            <span class="font-medium text-gray-800">{{ t('portal.profile.hideInDirectoryLabel') }}</span>
            <span class="block text-xs text-gray-500">{{ t('portal.profile.hideInDirectoryHint') }}</span>
          </span>
        </Checkbox>
      </fieldset>

      <fieldset ref="emailPrefsRef" class="rounded-md border border-gray-200 bg-gray-50 p-3">
        <legend class="px-1 text-sm font-semibold text-gray-700">{{ t('portal.profile.emailPrefsTitle') }}</legend>
        <p class="mb-3 text-xs text-gray-500">{{ t('portal.profile.emailPrefsHint') }}</p>
        <div v-if="emailPrefs" class="space-y-2">
          <label
            v-for="pref in emailPrefs"
            :key="pref.category"
            class="flex items-center justify-between gap-3"
          >
            <span class="text-sm text-gray-700">
              {{ pref.category === 'broadcast' ? t('portal.profile.emailPrefBroadcast') : pref.category }}
            </span>
            <button
              type="button"
              :class="[
                'relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors focus:outline-none focus:ring-2 focus:ring-blue-500 focus:ring-offset-2',
                pref.email_enabled ? 'bg-blue-600' : 'bg-gray-200',
              ]"
              :aria-checked="pref.email_enabled"
              role="switch"
              :aria-label="pref.category === 'broadcast' ? t('portal.profile.emailPrefBroadcast') : pref.category"
              @click="updateEmailPref({ category: pref.category, email_enabled: !pref.email_enabled })"
            >
              <span
                :class="[
                  'pointer-events-none inline-block h-5 w-5 rounded-full bg-white shadow ring-0 transition-transform',
                  pref.email_enabled ? 'translate-x-5' : 'translate-x-0',
                ]"
              />
            </button>
          </label>
        </div>
      </fieldset>

      <div v-if="privacyConsentNeeded" class="rounded-md border border-amber-200 bg-amber-50 p-3">
        <Checkbox v-model="privacyAgreed">
          <span class="text-amber-900">
            {{ t('portal.profile.privacyAgreePrefix') }}
            <RouterLink to="/portal/privacy-policy" class="font-semibold underline">
              {{ t('portal.profile.privacyAgreeLink') }}
            </RouterLink>
            <span v-if="privacyDoc?.version"> (v{{ privacyDoc.version }})</span>
          </span>
        </Checkbox>
      </div>

      <button
        type="submit"
        :disabled="isSaving || !canSubmit"
        class="rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white shadow-sm hover:bg-blue-700 disabled:opacity-50"
      >
        {{ isSaving ? t('portal.profile.saving') : t('common.save') }}
      </button>
    </form>
  </div>
</template>
