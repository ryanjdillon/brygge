import { ref, readonly } from 'vue'

export interface Toast {
  id: number
  type: 'success' | 'error' | 'info'
  message: string
}

const toasts = ref<Toast[]>([])
let nextId = 0

export function useToast() {
  function show(type: Toast['type'], message: string, duration = 4000) {
    const id = nextId++
    toasts.value.push({ id, type, message })
    if (duration > 0) {
      setTimeout(() => dismiss(id), duration)
    }
  }

  function dismiss(id: number) {
    toasts.value = toasts.value.filter((t) => t.id !== id)
  }

  function success(message: string, duration?: number) {
    show('success', message, duration)
  }

  function error(message: string, duration?: number) {
    show('error', message, duration)
  }

  function info(message: string, duration?: number) {
    show('info', message, duration)
  }

  return {
    toasts: readonly(toasts),
    show,
    dismiss,
    success,
    error,
    info,
  }
}
