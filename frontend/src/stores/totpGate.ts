import { ref } from 'vue'
import { defineStore } from 'pinia'

// totpGate carries cross-component state for the per-action step-up
// modal. The API client triggers .open(); the modal mounts in App.vue,
// watches `pending`, and resolves the promise when the user verifies
// (or rejects when they cancel). The API client then replays the
// original request.
//
// `warnExpiring` is a separate signal raised proactively about a minute
// before the fresh-TOTP window lapses, so the user can choose to
// re-verify before any in-flight admin work fails.
export const useTotpGateStore = defineStore('totp-gate', () => {
  const pending = ref(false)
  const warnExpiring = ref(false)
  let resolve: ((ok: boolean) => void) | null = null

  function open(): Promise<boolean> {
    if (pending.value) {
      return new Promise((res) => {
        const prev = resolve
        resolve = (ok) => {
          prev?.(ok)
          res(ok)
        }
      })
    }
    pending.value = true
    return new Promise((res) => {
      resolve = res
    })
  }

  function settle(ok: boolean) {
    pending.value = false
    resolve?.(ok)
    resolve = null
    if (ok) warnExpiring.value = false
  }

  function showExpiringWarning() {
    if (!pending.value) warnExpiring.value = true
  }

  function dismissExpiringWarning() {
    warnExpiring.value = false
  }

  return {
    pending,
    warnExpiring,
    open,
    settle,
    showExpiringWarning,
    dismissExpiringWarning,
  }
})
