<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'

const { t } = useI18n()
const { fetchApi } = useApi()
const queryClient = useQueryClient()

interface Profile {
  name: string
  email: string
  phone: string
  address: {
    street: string
    postalCode: string
    city: string
  }
  isLocal: boolean
}

const form = ref<Profile>({
  name: '',
  email: '',
  phone: '',
  address: { street: '', postalCode: '', city: '' },
  isLocal: false,
})

const toast = ref<{ type: 'success' | 'error'; message: string } | null>(null)

const { data: profile, isLoading } = useQuery({
  queryKey: ['portal', 'profile'],
  queryFn: () => fetchApi<Profile>('/api/v1/members/me'),
})

watch(profile, (p) => {
  if (p) {
    form.value = {
      name: p.name,
      email: p.email,
      phone: p.phone,
      address: { ...p.address },
      isLocal: p.isLocal,
    }
  }
}, { immediate: true })

const { mutate: saveProfile, isPending: isSaving } = useMutation({
  mutationFn: () =>
    fetchApi<Profile>('/api/v1/members/me', {
      method: 'PUT',
      body: JSON.stringify({
        name: form.value.name,
        email: form.value.email,
        phone: form.value.phone,
        address: form.value.address,
      }),
    }),
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
            :placeholder="t('portal.profile.street')"
            class="block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
          <div class="grid grid-cols-2 gap-3">
            <input
              v-model="form.address.postalCode"
              type="text"
              :placeholder="t('portal.profile.postalCode')"
              class="block w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
            <input
              v-model="form.address.city"
              type="text"
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

      <button
        type="submit"
        :disabled="isSaving"
        class="rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white shadow-sm hover:bg-blue-700 disabled:opacity-50"
      >
        {{ isSaving ? t('portal.profile.saving') : t('common.save') }}
      </button>
    </form>
  </div>
</template>
