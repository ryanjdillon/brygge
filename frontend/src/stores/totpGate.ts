import { ref } from 'vue'
import { defineStore } from 'pinia'

// totpGate carries cross-component state for the per-action 5-minute
// step-up modal. The API client triggers .open(); the modal mounts
// in App.vue, watches `pending`, and resolves the promise when the
// user verifies (or rejects when they cancel). The API client then
// replays the original request.
export const useTotpGateStore = defineStore('totp-gate', () => {
  const pending = ref(false)
  let resolve: ((ok: boolean) => void) | null = null

  function open(): Promise<boolean> {
    if (pending.value) {
      // A modal is already open — chain onto the existing promise.
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
  }

  return { pending, open, settle }
})
