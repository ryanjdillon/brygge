import { describe, it, expect, vi, afterEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import EconomySettingsView from '@/views/admin/EconomySettingsView.vue'

const totpAwareFetch = vi.hoisted(() => vi.fn())
vi.mock('@/composables/useFreshTotp', () => ({
  useFreshTotp: () => ({ ensureFreshTotp: async () => true, totpAwareFetch }),
}))
vi.mock('@/composables/useCan', () => ({
  useCan: () => ({ can: () => true }),
}))

const settings = {
  has_faktura_logo: false,
  kid_check_method: 'mod11',
  vipps_clearing_account: '2900',
  payment_fee_account: '7790',
  online_max_amount: 2500,
  vipps_auto_assign: false,
  vipps_auto_assign_tie_category: '',
  bank_auto_assign: false,
}

describe('EconomySettingsView online payment settings', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    totpAwareFetch.mockReset()
  })

  it('reads and saves the provider-neutral fee account and cap', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, json: async () => settings })))
    totpAwareFetch.mockResolvedValue({ ok: true, text: async () => '' })
    const wrapper = mountWithPlugins(EconomySettingsView, {
      global: { stubs: { BankAccountsPanel: true, ExemptionsSettingsPanel: true, FileInput: true } },
    })
    await flushPromises()

    const input = (label: string) =>
      wrapper.findAll('label').find((l) => l.text().startsWith(label))!.get('input').element as HTMLInputElement
    expect(input('admin.economySettings.paymentFeeAccount').value).toBe('7790')
    expect(input('admin.economySettings.onlineMaxAmount').value).toBe('2500')

    const save = wrapper
      .findAll('button')
      .find((b) => b.text() === 'common.save' && b.element.closest('fieldset')?.textContent?.includes('onlineMaxAmount'))
    await save!.trigger('click')
    await flushPromises()

    const body = JSON.parse(String(totpAwareFetch.mock.calls[0][1].body))
    expect(body).toMatchObject({ payment_fee_account: '7790', online_max_amount: 2500 })
    expect(body).not.toHaveProperty('vipps_max_amount')
    expect(body).not.toHaveProperty('vipps_fee_account')
  })
})
