<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { RouterLink } from 'vue-router'
import { useApiClient, unwrap } from '@/lib/apiClient'
import { useLegalDocument, useMyConsents, useRecordConsent } from '@/composables/useGdpr'

const { t } = useI18n()
const client = useApiClient()
const queryClient = useQueryClient()

interface ProfileForm {
  name: string
  email: string
  phone: string
  address: { street: string; postalCode: string; city: string }
  isLocal: boolean
  hideInDirectory: boolean
}

const form = ref<ProfileForm>({
  name: '',
  email: '',
  phone: '',
  address: { street: '', postalCode: '', city: '' },
  isLocal: false,
  hideInDirectory: false,
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
      } as any,
    }))
  },
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['portal', 'profile'] })
    toast.value = { type: 'success', message: t('portal.profile.saveSuccess') }
    setTimeout(() => (toast.value = null), 3000)
  },
  onError: () => {
    toast.value = { type: 'error', message: t('portal.profile.saveError') }
    setTimeout(() => (toast.value = null), 3000)
  },
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
      <div>
        <label for="profile-name" class="block text-sm font-medium text-gray-700">
          {{ t('portal.profile.fullName') }}
        </label>
        <input
          id="profile-name"
          v-model="form.name"
          type="text"
          required
          class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        />
      </div>

      <div>
        <label for="profile-email" class="block text-sm font-medium text-gray-700">
          {{ t('portal.profile.email') }}
        </label>
        <input
          id="profile-email"
          v-model="form.email"
          type="email"
          required
          class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        />
      </div>

      <div>
        <label for="profile-phone" class="block text-sm font-medium text-gray-700">
          {{ t('portal.profile.phone') }}
        </label>
        <input
          id="profile-phone"
          v-model="form.phone"
          type="tel"
          class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        />
      </div>

      <fieldset>
        <legend class="text-sm font-medium text-gray-700">{{ t('portal.profile.address') }}</legend>
        <div class="mt-2 space-y-3">
          <input
            v-model="form.address.street"
            type="text"
            :aria-label="t('portal.profile.street')"
            :placeholder="t('portal.profile.street')"
            class="block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
          <div class="grid grid-cols-2 gap-3">
            <input
              v-model="form.address.postalCode"
              type="text"
              :aria-label="t('portal.profile.postalCode')"
              :placeholder="t('portal.profile.postalCode')"
              class="block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
            <input
              v-model="form.address.city"
              type="text"
              :aria-label="t('portal.profile.city')"
              :placeholder="t('portal.profile.city')"
              class="block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
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

      <fieldset class="rounded-md border border-gray-200 bg-gray-50 p-3">
        <legend class="px-1 text-sm font-semibold text-gray-700">{{ t('portal.profile.privacyTitle') }}</legend>
        <label class="flex items-start gap-3 text-sm">
          <input
            v-model="form.hideInDirectory"
            type="checkbox"
            class="mt-0.5 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
          />
          <span>
            <span class="font-medium text-gray-800">{{ t('portal.profile.hideInDirectoryLabel') }}</span>
            <span class="block text-xs text-gray-500">{{ t('portal.profile.hideInDirectoryHint') }}</span>
          </span>
        </label>
      </fieldset>

      <div v-if="privacyConsentNeeded" class="rounded-md border border-amber-200 bg-amber-50 p-3">
        <label class="flex items-start gap-3 text-sm">
          <input
            v-model="privacyAgreed"
            type="checkbox"
            required
            class="mt-0.5 rounded border-amber-300 text-amber-600 focus:ring-amber-500"
          />
          <span class="text-amber-900">
            {{ t('portal.profile.privacyAgreePrefix') }}
            <RouterLink to="/portal/privacy-policy" class="font-semibold underline">
              {{ t('portal.profile.privacyAgreeLink') }}
            </RouterLink>
            <span v-if="privacyDoc?.version"> (v{{ privacyDoc.version }})</span>
          </span>
        </label>
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
