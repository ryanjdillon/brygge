import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import VippsQueueView from '@/views/admin/VippsQueueView.vue'

// A synthetic unclassified charge with a message and one message rule. A
// rule picks the revenue account the payout confirm books a charge to, so
// the server gates its writes on the money role and a fresh step-up (BRY-676).
const { ensureFreshTotp, askConfirm, fetchApi, icon, areas } = vi.hoisted(() => ({
  ensureFreshTotp: vi.fn(),
  askConfirm: vi.fn(),
  fetchApi: vi.fn(),
  icon: { template: '<span />' },
  areas: { writable: ['money', 'ledger'] as string[] },
}))

vi.mock('lucide-vue-next', () => ({ UserPlus: icon, Tag: icon, Trash2: icon, RefreshCw: icon, Inbox: icon }))
vi.mock('@/composables/useFreshTotp', () => ({ useFreshTotp: () => ({ ensureFreshTotp }) }))
vi.mock('@/composables/useCan', () => ({ useCan: () => ({ can: (area: string) => areas.writable.includes(area) }) }))
vi.mock('@/stores/confirm', () => ({ useConfirm: () => askConfirm }))
vi.mock('@/composables/useApi', () => ({ useApi: () => ({ fetchApi }) }))

const RULE_ID = '00000000-0000-0000-0000-0000000000b1'

function respond(url: string, init?: RequestInit) {
  if (init?.method) return Promise.resolve(null)
  if (url.endsWith('/vipps/queue')) {
    return Promise.resolve([
      { id: 'q1', booking_date: '2026-09-01', amount: 300, customer_name: 'SYN PAYER', message: 'synthetic dugnad', msn: '1', import_id: 'i1' },
    ])
  }
  if (url.endsWith('/message-rules')) {
    return Promise.resolve([{ id: RULE_ID, pattern: 'synthetic', account_code: '3200', priority: 0, created_at: '2026-09-01T00:00:00Z' }])
  }
  return Promise.resolve([])
}

const writes = () => fetchApi.mock.calls.filter(([, init]) => init?.method)
const ruleButton = (wrapper: ReturnType<typeof mountWithPlugins>) =>
  wrapper.findAll('button').find((b) => b.text() === 'Regel')

describe('VippsQueueView message-rule step-up', () => {
  beforeEach(() => {
    ensureFreshTotp.mockReset()
    askConfirm.mockReset()
    fetchApi.mockReset()
    fetchApi.mockImplementation(respond)
    areas.writable = ['money', 'ledger']
  })

  it('offers rule writes only to the money role, which the server gates them on', async () => {
    areas.writable = ['ledger']
    const wrapper = mountWithPlugins(VippsQueueView)
    await flushPromises()
    expect(wrapper.text()).toContain('synthetic')
    expect(ruleButton(wrapper)).toBeUndefined()
    expect(wrapper.findAll('button').some((b) => b.classes().includes('text-red-500'))).toBe(false)
  })

  it('asks for a fresh TOTP before opening the rule form', async () => {
    ensureFreshTotp.mockResolvedValue(false)
    const wrapper = mountWithPlugins(VippsQueueView, { global: { stubs: { teleport: true } } })
    await flushPromises()

    await ruleButton(wrapper)!.trigger('click')
    await flushPromises()
    expect(ensureFreshTotp).toHaveBeenCalledOnce()
    expect(wrapper.text()).not.toContain('Lag meldingsregel')

    ensureFreshTotp.mockResolvedValue(true)
    await ruleButton(wrapper)!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Lag meldingsregel')
    await wrapper.findAll('button').find((b) => b.text() === 'Lagre regel')!.trigger('click')
    await flushPromises()
    expect(writes()).toEqual([
      ['/api/v1/admin/accounting/vipps/message-rules', expect.objectContaining({ method: 'POST' })],
    ])
  })

  it('shows a refused save inside the rule form', async () => {
    ensureFreshTotp.mockResolvedValue(true)
    fetchApi.mockImplementation((url: string, init?: RequestInit) =>
      init?.method === 'POST'
        ? Promise.reject(new Error('Kontokoden er ikkje ein aktiv konto i klubben.'))
        : respond(url, init),
    )
    const wrapper = mountWithPlugins(VippsQueueView, { global: { stubs: { teleport: true } } })
    await flushPromises()
    await ruleButton(wrapper)!.trigger('click')
    await flushPromises()
    await wrapper.findAll('button').find((b) => b.text() === 'Lagre regel')!.trigger('click')
    await flushPromises()
    const alert = wrapper.get('[role="alert"]')
    expect(alert.text()).toBe('Kontokoden er ikkje ein aktiv konto i klubben.')
    // Still in the open form, not on the page behind it.
    expect(wrapper.text()).toContain('Lag meldingsregel')
  })

  it('asks for a fresh TOTP, then a confirm, before deleting a rule', async () => {
    ensureFreshTotp.mockResolvedValue(false)
    const wrapper = mountWithPlugins(VippsQueueView)
    await flushPromises()
    // No aliases are listed, so the only delete button is the rule's.
    const del = wrapper.findAll('button').find((b) => b.classes().includes('text-red-500'))!
    expect(del.attributes('aria-label')).toBe('Slett meldingsregel synthetic')

    await del.trigger('click')
    await flushPromises()
    expect(ensureFreshTotp).toHaveBeenCalledOnce()
    expect(askConfirm).not.toHaveBeenCalled()
    expect(writes()).toEqual([])

    ensureFreshTotp.mockResolvedValue(true)
    askConfirm.mockResolvedValue(false)
    await del.trigger('click')
    await flushPromises()
    expect(askConfirm).toHaveBeenCalledOnce()
    expect(writes()).toEqual([])

    askConfirm.mockResolvedValue(true)
    await del.trigger('click')
    await flushPromises()
    expect(writes()).toEqual([
      [`/api/v1/admin/accounting/vipps/message-rules/${RULE_ID}`, { method: 'DELETE' }],
    ])
  })
})
