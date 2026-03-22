<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { LogIn } from 'lucide-vue-next'

const { t } = useI18n()
const auth = useAuthStore()

const email = ref('')
const submitting = ref(false)
const magicLinkSent = ref(false)

async function handleSubmit() {
  submitting.value = true
  const ok = await auth.requestMagicLink(email.value)
  submitting.value = false
  if (ok) {
    magicLinkSent.value = true
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
        <div
          v-if="magicLinkSent"
          class="rounded-md bg-green-50 px-4 py-3 text-sm text-green-700"
          role="status"
        >
          {{ t('login.magicLinkSent') }}
        </div>

        <div
          v-if="auth.loginError"
          class="rounded-md bg-red-50 px-4 py-3 text-sm text-red-700"
          role="alert"
        >
          {{ auth.loginError }}
        </div>

        <form
          v-if="!magicLinkSent"
          class="space-y-5"
          @submit.prevent="handleSubmit"
        >
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
              :placeholder="t('login.emailPlaceholder')"
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <button
            type="submit"
            :disabled="submitting"
            class="w-full rounded-md bg-blue-600 px-4 py-2.5 text-sm font-medium text-white hover:bg-blue-700 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:ring-offset-2 disabled:opacity-50"
          >
            {{ submitting ? t('login.sending') : t('login.sendLink') }}
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
