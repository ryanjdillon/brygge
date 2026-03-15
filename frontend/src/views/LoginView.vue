<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { LogIn, ChevronDown, ChevronUp } from 'lucide-vue-next'

const { t } = useI18n()
const router = useRouter()
const auth = useAuthStore()

const email = ref('')
const password = ref('')
const submitting = ref(false)
const showEmailForm = ref(false)
const vippsEnabled = ref(false)

onMounted(async () => {
  try {
    const res = await fetch('/api/v1/auth/vipps/status')
    if (res.ok) {
      const data = await res.json()
      vippsEnabled.value = data.enabled
    }
  } catch {
    // Vipps not available
  }
})

function loginWithVipps() {
  window.location.href = '/api/v1/auth/vipps/login'
}

async function handleEmailLogin() {
  submitting.value = true
  const ok = await auth.login(email.value, password.value)
  submitting.value = false
  if (ok) {
    const redirect = auth.hasRole('admin') || auth.hasRole('board') ? '/admin' : '/portal'
    router.push(redirect)
  }
}
</script>

<template>
  <div class="flex min-h-[calc(100vh-8rem)] items-center justify-center px-4">
    <div class="w-full max-w-sm">
      <div class="text-center">
        <LogIn class="mx-auto h-10 w-10 text-blue-600" />
        <h1 class="mt-4 text-2xl font-bold text-gray-900">{{ t('login.title') }}</h1>
      </div>

      <div class="mt-8 space-y-5">
        <button
          v-if="vippsEnabled"
          class="flex w-full items-center justify-center gap-3 rounded-md px-4 py-3 text-base font-semibold text-white shadow-sm transition hover:opacity-90"
          style="background-color: #ff5b24"
          @click="loginWithVipps"
        >
          <svg viewBox="0 0 28 28" class="h-6 w-6" fill="currentColor">
            <path d="M6.36 17.59c-.78 0-1.57-.37-2.05-1.07L.2 10.44a1.1 1.1 0 0 1 .3-1.52 1.1 1.1 0 0 1 1.52.3l4.11 6.08c.15.22.35.27.5.27.16 0 .34-.06.49-.27l9.54-14.14a1.1 1.1 0 0 1 1.52-.3 1.1 1.1 0 0 1 .3 1.52L9.43 16.51c-.47.7-1.26 1.07-2.04 1.08h-.03z" transform="translate(3 5)" />
          </svg>
          {{ t('login.vipps') }}
        </button>

        <div v-if="vippsEnabled" class="relative">
          <div class="absolute inset-0 flex items-center">
            <div class="w-full border-t border-gray-200" />
          </div>
          <div class="relative flex justify-center text-sm">
            <span class="bg-white px-3 text-gray-400">{{ t('login.or') }}</span>
          </div>
        </div>

        <button
          v-if="vippsEnabled"
          class="flex w-full items-center justify-center gap-1.5 text-sm text-gray-500 hover:text-gray-700"
          @click="showEmailForm = !showEmailForm"
        >
          {{ t('login.otherMethods') }}
          <component :is="showEmailForm ? ChevronUp : ChevronDown" class="h-4 w-4" />
        </button>

        <form
          v-if="showEmailForm || !vippsEnabled"
          class="space-y-5"
          @submit.prevent="handleEmailLogin"
        >
          <div
            v-if="auth.loginError"
            class="rounded-md bg-red-50 px-4 py-3 text-sm text-red-700"
            role="alert"
          >
            {{ auth.loginError }}
          </div>

          <div>
            <label for="email" class="block text-sm font-medium text-gray-700">
              {{ t('login.email') }}
            </label>
            <input
              id="email"
              v-model="email"
              type="email"
              required
              autocomplete="email"
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <div>
            <label for="password" class="block text-sm font-medium text-gray-700">
              {{ t('login.password') }}
            </label>
            <input
              id="password"
              v-model="password"
              type="password"
              required
              autocomplete="current-password"
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <button
            type="submit"
            :disabled="submitting"
            class="w-full rounded-md bg-blue-600 px-4 py-2.5 text-sm font-medium text-white hover:bg-blue-700 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:ring-offset-2 disabled:opacity-50"
          >
            {{ submitting ? t('login.submitting') : t('login.submit') }}
          </button>
        </form>
      </div>

      <p class="mt-6 text-center text-sm text-gray-500">
        {{ t('login.noAccount') }}
        <router-link to="/join" class="font-medium text-blue-600 hover:text-blue-500">
          {{ t('login.joinLink') }}
        </router-link>
      </p>
    </div>
  </div>
</template>
