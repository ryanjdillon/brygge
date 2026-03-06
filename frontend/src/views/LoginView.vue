<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { LogIn } from 'lucide-vue-next'

const { t } = useI18n()
const router = useRouter()
const auth = useAuthStore()

const email = ref('')
const password = ref('')
const submitting = ref(false)

async function handleSubmit() {
  submitting.value = true
  const ok = await auth.login(email.value, password.value)
  submitting.value = false
  if (ok) {
    const redirect = auth.hasRole('admin') || auth.hasRole('styre') ? '/admin' : '/portal'
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

      <form class="mt-8 space-y-5" @submit.prevent="handleSubmit">
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

      <p class="mt-6 text-center text-sm text-gray-500">
        {{ t('login.noAccount') }}
        <router-link to="/join" class="font-medium text-blue-600 hover:text-blue-500">
          {{ t('login.joinLink') }}
        </router-link>
      </p>
    </div>
  </div>
</template>
