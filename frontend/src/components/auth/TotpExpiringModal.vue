<script setup lang="ts">
import { computed, ref, watch, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Clock } from 'lucide-vue-next'
import { useTotpGateStore } from '@/stores/totpGate'
import { useAuthStore, TOTP_ACTION_FRESH_MS } from '@/stores/auth'

const { t } = useI18n()
const gate = useTotpGateStore()
const auth = useAuthStore()

const visible = computed(() => gate.warnExpiring && !gate.pending)

// Absolute moment the per-action freshness window lapses.
const expiresAtMs = computed(() => {
  const v = auth.user?.totpVerifiedAt
  return v ? v.getTime() + TOTP_ACTION_FRESH_MS : null
})

const remainingMs = ref(0)
let ticker: ReturnType<typeof setInterval> | null = null

function stop() {
  if (ticker) {
    clearInterval(ticker)
    ticker = null
  }
}

function tick() {
  const end = expiresAtMs.value
  if (end == null) {
    remainingMs.value = 0
    return
  }
  remainingMs.value = Math.max(0, end - Date.now())
  if (remainingMs.value <= 0) {
    // Window has lapsed — the warning is now pointless (clicking
    // "dismiss" wouldn't keep TOTP fresh anyway), so close it. The
    // next sensitive action re-triggers the full step-up modal.
    stop()
    gate.dismissExpiringWarning()
  }
}

watch(
  visible,
  (open) => {
    stop()
    if (open) {
      tick()
      ticker = setInterval(tick, 1000)
    }
  },
  { immediate: true },
)

onUnmounted(stop)

const countdown = computed(() => {
  const total = Math.ceil(remainingMs.value / 1000)
  const m = Math.floor(total / 60)
  const s = total % 60
  return `${m}:${s.toString().padStart(2, '0')}`
})

function dismiss() {
  gate.dismissExpiringWarning()
}
</script>

<template>
  <div
    v-if="visible"
    class="fixed inset-0 z-40 flex items-center justify-center bg-black/40 p-4"
    role="dialog"
    aria-modal="true"
    @click.self="dismiss"
    @keydown.esc="dismiss"
  >
    <div class="w-full max-w-sm rounded-lg bg-white p-5 shadow-xl">
      <div class="flex items-center justify-between border-b border-gray-100 pb-3">
        <div class="flex items-center gap-2">
          <Clock class="h-5 w-5 text-amber-600" />
          <h2 class="text-base font-semibold text-gray-900">
            {{ t('totpExpiring.title') }}
          </h2>
        </div>
        <span
          class="rounded bg-amber-50 px-2 py-1 font-mono text-sm font-semibold tabular-nums text-amber-700"
          aria-live="polite"
        >
          {{ t('totpExpiring.countdown', { time: countdown }) }}
        </span>
      </div>
      <p class="mt-3 text-sm text-gray-600">
        {{ t('totpExpiring.body') }}
      </p>
      <div class="mt-4 flex justify-end">
        <button
          type="button"
          class="rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white hover:bg-blue-700"
          @click="dismiss"
        >
          {{ t('totpExpiring.continue') }}
        </button>
      </div>
    </div>
  </div>
</template>
