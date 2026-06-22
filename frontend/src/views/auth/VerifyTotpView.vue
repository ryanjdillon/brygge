<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ShieldCheck, KeyRound } from 'lucide-vue-next'

import { useAuthStore } from '@/stores/auth'
import { useTotp, TotpError } from '@/composables/useTotp'

const router = useRouter()
const route = useRoute()
const { t } = useI18n()
const totp = useTotp()
const auth = useAuthStore()

// If we land here without 2FA enrolled, the verify form is a
// dead-end — there's no code to type. Redirect into enrollment so
// the user has a path forward instead of staring at an empty input.
onMounted(async () => {
  await auth.ready
  if (auth.user && !auth.user.totpEnabled) {
    const next = (route.query.next as string) || '/admin'
    router.replace({ path: '/portal/security', query: { next } })
  }
})

const code = ref('')
const useRecovery = ref(false)
const failedAttempts = ref(0)
const error = ref<string | null>(null)
const busy = ref(false)

const nextPath = computed(() => {
  const next = route.query.next
  if (typeof next === 'string' && next.startsWith('/')) {
    return next
  }
  return '/admin'
})

// After 3 failed authenticator-code attempts, suggest the recovery
// path proactively — typo-prone users find their way unstuck faster.
const suggestRecovery = computed(() => failedAttempts.value >= 3 && !useRecovery.value)

async function submit() {
  error.value = null
  const value = code.value.trim()
  if (!value) {
    error.value = t('totpVerify.errors.required')
    return
  }
  busy.value = true
  try {
    if (useRecovery.value) {
      await totp.recover(value)
    } else {
      await totp.verify(value)
    }
    router.replace(nextPath.value)
  } catch (e) {
    failedAttempts.value++
    if (e instanceof TotpError && e.message) {
      error.value = friendlyError(e.message)
    } else if (e instanceof Error) {
      error.value = e.message
    } else {
      error.value = t('totpVerify.errors.failed')
    }
  } finally {
    busy.value = false
  }
}

function switchToRecovery() {
  useRecovery.value = true
  code.value = ''
  error.value = null
}

function friendlyError(backendCode: string): string {
  switch (backendCode) {
    case 'invalid TOTP code':
      return t('totpVerify.errors.invalidCode')
    case 'invalid or already-used recovery code':
      return t('totpVerify.errors.invalidRecovery')
    default:
      return backendCode
  }
}
</script>

<template>
  <div class="min-h-screen bg-gray-50 px-4 py-12">
    <div class="mx-auto max-w-md rounded-lg border border-gray-200 bg-white p-6 shadow-sm">
      <div class="flex items-center gap-3 border-b border-gray-100 pb-4">
        <ShieldCheck class="h-7 w-7 text-brand-600" />
        <div>
          <h1 class="text-lg font-semibold text-gray-900">{{ t('totpVerify.title') }}</h1>
          <p class="text-sm text-gray-500">{{ t('totpVerify.subtitle') }}</p>
        </div>
      </div>

      <form class="mt-5 space-y-4" @submit.prevent="submit">
        <div>
          <label class="block text-sm font-medium text-gray-700" :for="useRecovery ? 'recovery-code' : 'totp-code'">
            {{ useRecovery ? t('totpVerify.recoveryLabel') : t('totpVerify.codeLabel') }}
          </label>
          <input
            v-if="!useRecovery"
            id="totp-code"
            v-model="code"
            type="text"
            inputmode="numeric"
            autocomplete="one-time-code"
            maxlength="6"
            autofocus
            class="mt-1 w-full rounded-md border border-gray-300 px-3 py-2 font-mono text-lg tracking-widest focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
          />
          <input
            v-else
            id="recovery-code"
            v-model="code"
            type="text"
            autocomplete="off"
            maxlength="9"
            placeholder="XXXX-XXXX"
            autofocus
            class="mt-1 w-full rounded-md border border-gray-300 px-3 py-2 font-mono text-lg tracking-widest focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
          />
        </div>

        <p v-if="error" class="rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">{{ error }}</p>

        <button
          type="submit"
          class="w-full rounded-md bg-brand-600 px-4 py-2 text-sm font-semibold text-white hover:bg-brand-700 disabled:opacity-50"
          :disabled="busy"
        >
          {{ busy ? t('common.loading') : t('totpVerify.submitButton') }}
        </button>

        <div class="text-center text-sm">
          <button
            v-if="!useRecovery"
            type="button"
            class="inline-flex items-center gap-1 text-gray-600 hover:text-gray-900"
            @click="switchToRecovery"
          >
            <KeyRound class="h-4 w-4" />
            {{ t('totpVerify.useRecovery') }}
          </button>
          <button
            v-else
            type="button"
            class="text-gray-600 hover:text-gray-900"
            @click="useRecovery = false; code = ''; error = null"
          >
            {{ t('totpVerify.useAuthenticator') }}
          </button>
        </div>

        <p v-if="suggestRecovery" class="rounded-md bg-amber-50 px-3 py-2 text-sm text-amber-800">
          {{ t('totpVerify.suggestRecovery') }}
        </p>
      </form>
    </div>
  </div>
</template>
