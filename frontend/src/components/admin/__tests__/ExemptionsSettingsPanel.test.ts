import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import ExemptionsSettingsPanel from '@/components/admin/ExemptionsSettingsPanel.vue'

// Role exemptions settings group (BRY-351): one row per board role from the
// server, rules added per role, saved behind a click-time TOTP step-up.

const ensureFreshTotp = vi.fn(async () => true)
const totpAwareFetch = vi.fn()

vi.mock('@/composables/useFreshTotp', () => ({
  useFreshTotp: () => ({ ensureFreshTotp, totpAwareFetch }),
}))

const config = {
  expense_account: '',
  rules: [] as unknown[],
  board_roles: ['chair', 'treasurer', 'board'],
}
const priceItems = [{ id: 'item-slipping', category: 'service', name: 'Synthetic slipping', is_active: true }]
const accounts = [
  { id: 'a1', code: '5000', name: 'Synthetic expense', account_type: 'expense', is_active: true },
  { id: 'a2', code: '3100', name: 'Synthetic revenue', account_type: 'revenue', is_active: true },
]

function json(body: unknown) {
  return Promise.resolve(new Response(JSON.stringify(body), { status: 200 }))
}

describe('ExemptionsSettingsPanel', () => {
  beforeEach(() => {
    ensureFreshTotp.mockClear()
    totpAwareFetch.mockReset().mockImplementation((_url: string, init: RequestInit) =>
      json(JSON.parse(init.body as string)),
    )
    vi.stubGlobal('fetch', vi.fn((url: string) => {
      if (url.includes('/exemptions')) return json(config)
      if (url.includes('/pricing')) return json(priceItems)
      return json(accounts)
    }))
  })
  afterEach(() => vi.unstubAllGlobals())

  it('lists the board roles and offers only expense accounts', async () => {
    const wrapper = mountWithPlugins(ExemptionsSettingsPanel)
    await flushPromises()
    expect(wrapper.findAll('tr[data-role]').map((r) => r.attributes('data-role'))).toEqual([
      'chair', 'treasurer', 'board',
    ])
    const options = wrapper.find('[data-testid="exemption-account"]').findAll('option').map((o) => o.element.value)
    expect(options).toEqual(['', '5000'])
  })

  it('refuses to save rules without an expense account', async () => {
    const wrapper = mountWithPlugins(ExemptionsSettingsPanel)
    await flushPromises()
    await wrapper.find('tr[data-role="treasurer"] [data-testid="exemption-add"]').setValue('c:slip_fee')
    await wrapper.find('[data-testid="exemption-save"]').trigger('click')
    await flushPromises()
    expect(totpAwareFetch).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.economySettings.exemptions.accountRequired')
  })

  it('saves category and item rules after the step-up', async () => {
    const wrapper = mountWithPlugins(ExemptionsSettingsPanel)
    await flushPromises()
    await wrapper.find('tr[data-role="treasurer"] [data-testid="exemption-add"]').setValue('c:slip_fee')
    await wrapper.find('tr[data-role="chair"] [data-testid="exemption-add"]').setValue('i:item-slipping')
    await wrapper.find('[data-testid="exemption-account"]').setValue('5000')
    expect(wrapper.findAll('[data-testid="exemption-chip"]')).toHaveLength(2)

    await wrapper.find('[data-testid="exemption-save"]').trigger('click')
    await flushPromises()
    expect(ensureFreshTotp).toHaveBeenCalled()
    const [url, init] = totpAwareFetch.mock.calls[0]
    expect(url).toBe('/api/v1/admin/settings/economy/exemptions')
    expect(init.method).toBe('PUT')
    expect(JSON.parse(init.body)).toEqual({
      expense_account: '5000',
      rules: [
        { role: 'treasurer', category: 'slip_fee' },
        { role: 'chair', price_item_id: 'item-slipping' },
      ],
    })
  })
})
