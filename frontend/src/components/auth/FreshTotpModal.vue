<script setup lang="ts">
import { ref, computed, watch, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { ShieldCheck, X, KeyRound } from 'lucide-vue-next'

import { useTotp, TotpError } from '@/composables/useTotp'
import { useTotpGateStore } from '@/stores/totpGate'

const { t } = useI18n()
const totp = useTotp()
const gate = useTotpGateStore()

const code = ref('')
const useRecovery = ref(false)
const failedAttempts = ref(0)
const error = ref<string | null>(null)
const busy = ref(false)
const inputRef = ref<HTMLInputElement | null>(null)

const suggestRecovery = computed(() => failedAttempts.value >= 3 && !useRecovery.value)

// Reset whenever the modal opens; autofocus the input.
watch(
  () => gate.pending,
  async (open) => {
    if (open) {
      code.value = ''
      useRecovery.value = false
      failedAttempts.value = 0
      error.value = null
      await nextTick()
      inputRef.value?.focus()
    }
  },
)

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
    gate.settle(true)
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

function cancel() {
  gate.settle(false)
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
  <div
    v-if="gate.pending"
    class="fixed inset-0 z-[100] flex items-center justify-center bg-black/50 p-4"
    role="dialog"
    aria-modal="true"
    @keydown.esc="cancel"
  >
    <div class="w-full max-w-sm rounded-lg bg-white p-5 shadow-xl">
      <div class="flex items-start justify-between border-b border-gray-100 pb-3">
        <div class="flex items-center gap-2">
          <ShieldCheck class="h-5 w-5 text-brand-600" />
          <h2 class="text-base font-semibold text-gray-900">{{ t('totpVerify.modalTitle') }}</h2>
        </div>
        <button class="text-gray-400 hover:text-gray-600" :aria-label="t('common.close')" @click="cancel">
          <X class="h-5 w-5" />
        </button>
      </div>

      <p class="mt-3 text-sm text-gray-600">{{ t('totpVerify.modalSubtitle') }}</p>

      <form class="mt-4 space-y-3" @submit.prevent="submit">
        <input
          v-if="!useRecovery"
          ref="inputRef"
          v-model="code"
          type="text"
          inputmode="numeric"
          autocomplete="one-time-code"
          maxlength="6"
          class="w-full rounded-md border border-gray-300 px-3 py-2 font-mono text-lg tracking-widest focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
        />
        <input
          v-else
          ref="inputRef"
          v-model="code"
          type="text"
          autocomplete="off"
          maxlength="9"
          placeholder="XXXX-XXXX"
          class="w-full rounded-md border border-gray-300 px-3 py-2 font-mono text-lg tracking-widest focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
        />

        <p v-if="error" class="rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">{{ error }}</p>

        <div class="flex gap-2">
          <button
            type="button"
            class="flex-1 rounded-md bg-white px-3 py-2 text-sm font-semibold text-gray-700 ring-1 ring-gray-300 hover:bg-gray-50"
            @click="cancel"
          >
            {{ t('common.cancel') }}
          </button>
          <button
            type="submit"
            class="flex-1 rounded-md bg-brand-600 px-3 py-2 text-sm font-semibold text-white hover:bg-brand-700 disabled:opacity-50"
            :disabled="busy"
          >
            {{ busy ? t('common.loading') : t('totpVerify.submitButton') }}
          </button>
        </div>

        <button
          v-if="!useRecovery"
          type="button"
          class="inline-flex items-center gap-1 text-xs text-gray-500 hover:text-gray-700"
          @click="switchToRecovery"
        >
          <KeyRound class="h-3.5 w-3.5" />
          {{ t('totpVerify.useRecovery') }}
        </button>

        <p v-if="suggestRecovery" class="rounded-md bg-amber-50 px-3 py-2 text-xs text-amber-800">
          {{ t('totpVerify.suggestRecovery') }}
        </p>
      </form>
    </div>
  </div>
</template>
