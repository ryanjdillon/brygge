import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import VippsQueueView from '@/views/admin/VippsQueueView.vue'

// A synthetic unclassified charge and one learned alias.
const { ensureFreshTotp, fetchApi, icon, areas } = vi.hoisted(() => ({
  ensureFreshTotp: vi.fn(),
  fetchApi: vi.fn(),
  icon: { template: '<span />' },
  areas: { writable: ['money', 'ledger'] as string[] },
}))

vi.mock('lucide-vue-next', () => ({ UserPlus: icon, Tag: icon, Trash2: icon, RefreshCw: icon, Inbox: icon }))
vi.mock('@/composables/useFreshTotp', () => ({ useFreshTotp: () => ({ ensureFreshTotp }) }))
vi.mock('@/composables/useCan', () => ({ useCan: () => ({ can: (area: string) => areas.writable.includes(area) }) }))
vi.mock('@/composables/useApi', () => ({ useApi: () => ({ fetchApi }) }))

const ALIAS_ID = '00000000-0000-0000-0000-0000000000a1'

function respond(url: string, init?: RequestInit) {
  if (init?.method) return Promise.resolve(null)
  if (url.endsWith('/vipps/queue')) {
    return Promise.resolve([
      { id: 'q1', booking_date: '2026-09-01', amount: 300, customer_name: 'SYN PAYER', message: '', msn: '1', import_id: 'i1' },
    ])
  }
  if (url.endsWith('/customer-aliases')) {
    return Promise.resolve([{ id: ALIAS_ID, customer_name: 'SYN OTHER', user_id: 'member-1', created_at: '2026-09-01T00:00:00Z' }])
  }
  if (url.endsWith('/admin/users')) return Promise.resolve([{ id: 'member-1', full_name: 'Synthetic Fortoy', email: 'fortoy@example.invalid' }])
  return Promise.resolve([])
}

const writes = () => fetchApi.mock.calls.filter(([, init]) => init?.method)

describe('VippsQueueView alias step-up', () => {
  beforeEach(() => {
    ensureFreshTotp.mockReset()
    fetchApi.mockReset()
    fetchApi.mockImplementation(respond)
    areas.writable = ['money', 'ledger']
  })

  it('offers alias writes only to the money role, which the server gates them on', async () => {
    areas.writable = ['ledger']
    const wrapper = mountWithPlugins(VippsQueueView)
    await flushPromises()
    expect(wrapper.findAll('button').some((b) => b.text() === 'Alias')).toBe(false)
    expect(wrapper.findAll('button').some((b) => b.classes().includes('text-red-500'))).toBe(false)
  })

  it('asks for a fresh TOTP before opening the alias form', async () => {
    ensureFreshTotp.mockResolvedValue(false)
    const wrapper = mountWithPlugins(VippsQueueView, { global: { stubs: { teleport: true } } })
    await flushPromises()

    await wrapper.findAll('button').find((b) => b.text() === 'Alias')!.trigger('click')
    await flushPromises()
    expect(ensureFreshTotp).toHaveBeenCalledOnce()
    expect(wrapper.text()).not.toContain('Knytt betalar til medlem')

    ensureFreshTotp.mockResolvedValue(true)
    await wrapper.findAll('button').find((b) => b.text() === 'Alias')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Knytt betalar til medlem')
    await wrapper.get('select').setValue('member-1')
    await wrapper.findAll('button').find((b) => b.text() === 'Lagre alias')!.trigger('click')
    await flushPromises()
    expect(writes()).toEqual([
      ['/api/v1/admin/accounting/vipps/customer-aliases', expect.objectContaining({ method: 'POST' })],
    ])
  })

  it('asks for a fresh TOTP before deleting an alias, and deletes nothing without it', async () => {
    ensureFreshTotp.mockResolvedValue(false)
    const wrapper = mountWithPlugins(VippsQueueView)
    await flushPromises()
    const del = wrapper.findAll('button').find((b) => b.classes().includes('text-red-500'))!

    await del.trigger('click')
    await flushPromises()
    expect(ensureFreshTotp).toHaveBeenCalledOnce()
    expect(writes()).toEqual([])

    ensureFreshTotp.mockResolvedValue(true)
    await del.trigger('click')
    await flushPromises()
    expect(writes()).toEqual([
      [`/api/v1/admin/accounting/vipps/customer-aliases/${ALIAS_ID}`, { method: 'DELETE' }],
    ])
  })
})
