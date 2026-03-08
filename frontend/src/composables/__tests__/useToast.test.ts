import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useToast } from '@/composables/useToast'

describe('useToast', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    // Clear toasts between tests
    const { toasts, dismiss } = useToast()
    toasts.value.forEach((t) => dismiss(t.id))
  })

  it('adds a toast via show()', () => {
    const { show, toasts } = useToast()
    show('info', 'Hello')
    expect(toasts.value).toHaveLength(1)
    expect(toasts.value[0].message).toBe('Hello')
    expect(toasts.value[0].type).toBe('info')
  })

  it('auto-dismisses after duration', () => {
    const { success, toasts } = useToast()
    success('Done', 2000)
    expect(toasts.value).toHaveLength(1)

    vi.advanceTimersByTime(2000)
    expect(toasts.value).toHaveLength(0)
  })

  it('does not auto-dismiss when duration is 0', () => {
    const { error, toasts } = useToast()
    error('Persistent', 0)
    vi.advanceTimersByTime(10000)
    expect(toasts.value).toHaveLength(1)
  })

  it('manually dismisses a toast', () => {
    const { info, toasts, dismiss } = useToast()
    info('Test', 0)
    const id = toasts.value[0].id
    dismiss(id)
    expect(toasts.value).toHaveLength(0)
  })

  it('provides success/error/info convenience methods', () => {
    const { success, error, info, toasts } = useToast()
    success('s')
    error('e')
    info('i')
    const types = toasts.value.map((t) => t.type)
    expect(types).toContain('success')
    expect(types).toContain('error')
    expect(types).toContain('info')
  })
})
