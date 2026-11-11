import { describe, it, expect, vi, afterEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import EconomySettingsView from '@/views/admin/EconomySettingsView.vue'

const totpAwareFetch = vi.hoisted(() => vi.fn())
const ensureFreshTotp = vi.hoisted(() => vi.fn(async () => true))
const canMoney = vi.hoisted(() => ({ value: true }))
vi.mock('@/composables/useFreshTotp', () => ({
  useFreshTotp: () => ({ ensureFreshTotp, totpAwareFetch }),
}))
vi.mock('@/composables/useCan', () => ({
  useCan: () => ({ can: () => canMoney.value }),
}))

async function mountView(settings: Record<string, unknown> | null) {
  vi.stubGlobal('fetch', vi.fn(async () =>
    settings ? { ok: true, json: async () => settings } : { ok: false, status: 500, statusText: 'boom' }))
  const wrapper = mountWithPlugins(EconomySettingsView, {
    global: { stubs: { BankAccountsPanel: true, ExemptionsSettingsPanel: true, FileInput: true } },
  })
  await flushPromises()
  const box = wrapper.get('[data-testid="automatic-booking"]')
  return { wrapper, box, checkbox: box.get('input[type="checkbox"]'), save: box.get('button') }
}

// BRY-568: the master switch for the automatic booking run (ACC-13), and the
// bank auto-assign switch, both through SiteSettingSwitch.
describe('EconomySettingsView automatic booking switch', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    totpAwareFetch.mockReset()
    ensureFreshTotp.mockReset()
    ensureFreshTotp.mockImplementation(async () => true)
    canMoney.value = true
  })

  it('shows the stored value, the label and the hint', async () => {
    const on = await mountView({ automatic_booking: true })
    expect((on.checkbox.element as HTMLInputElement).checked).toBe(true)
    expect(on.box.text()).toContain('admin.economySettings.automaticBookingLabel')
    expect(on.box.text()).toContain('admin.economySettings.automaticBookingHint')

    const off = await mountView({})
    expect((off.checkbox.element as HTMLInputElement).checked).toBe(false)
  })

  it('comes before the payment accounts block', async () => {
    const { wrapper } = await mountView({})
    const html = wrapper.html()
    expect(html.indexOf('data-testid="automatic-booking"')).toBeLessThan(
      html.indexOf('admin.economySettings.paymentAccountsLabel'),
    )
  })

  it('saves only the switch after a fresh TOTP', async () => {
    totpAwareFetch.mockResolvedValue({ ok: true, text: async () => '' })
    const { box, checkbox, save } = await mountView({ automatic_booking: false })

    await checkbox.setValue(true)
    await save.trigger('click')
    await flushPromises()

    expect(ensureFreshTotp.mock.invocationCallOrder[0]).toBeLessThan(totpAwareFetch.mock.invocationCallOrder[0])
    expect(JSON.parse(String(totpAwareFetch.mock.calls[0][1].body))).toEqual({ automatic_booking: true })
    expect(box.text()).toContain('common.saved')
  })

  it('does not save when the fresh TOTP check is declined', async () => {
    ensureFreshTotp.mockResolvedValue(false)
    const { checkbox, save } = await mountView({ automatic_booking: false })

    await checkbox.setValue(true)
    await save.trigger('click')
    await flushPromises()

    expect(totpAwareFetch).not.toHaveBeenCalled()
  })

  it('shows a failed save in the page banner', async () => {
    totpAwareFetch.mockResolvedValue({ ok: false, status: 500, text: async () => 'boom' })
    const { wrapper, box, save } = await mountView({ automatic_booking: false })

    await save.trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('500 boom')
    expect(box.text()).not.toContain('common.saved')
  })

  it('cannot save after the settings failed to load, so it never turns the switch off blind', async () => {
    const { wrapper, save } = await mountView(null)
    expect(save.element.matches(':disabled')).toBe(true)
    expect(wrapper.get('[data-testid="bank-auto-assign"] button').element.matches(':disabled')).toBe(true)
  })

  it('puts the switch back and shows the error in its box and the banner when the save fails', async () => {
    totpAwareFetch.mockResolvedValue({ ok: false, status: 500, text: async () => 'boom' })
    const { wrapper, box, checkbox, save } = await mountView({ automatic_booking: false })

    await checkbox.setValue(true)
    await save.trigger('click')
    await flushPromises()

    expect((checkbox.element as HTMLInputElement).checked).toBe(false)
    expect(box.get('[role="alert"]').text()).toBe('500 boom')
    expect(wrapper.text().split('500 boom').length - 1).toBe(2)
  })

  it('is read-only for someone without the money permission', async () => {
    canMoney.value = false
    const { wrapper, checkbox, save } = await mountView({ automatic_booking: true })
    expect(wrapper.get('[data-testid="economy-settings-fields"]').attributes('disabled')).toBeDefined()
    expect(checkbox.element.matches(':disabled')).toBe(true)
    expect(save.element.matches(':disabled')).toBe(true)
  })

  it('wires the bank auto-assign switch to its own field', async () => {
    totpAwareFetch.mockResolvedValue({ ok: true, text: async () => '' })
    const { wrapper } = await mountView({ bank_auto_assign: true })
    const bank = wrapper.get('[data-testid="bank-auto-assign"]')
    expect((bank.get('input[type="checkbox"]').element as HTMLInputElement).checked).toBe(true)
    expect(bank.text()).toContain('admin.economySettings.bankAutoAssignHint')

    await bank.get('button').trigger('click')
    await flushPromises()

    expect(JSON.parse(String(totpAwareFetch.mock.calls[0][1].body))).toEqual({ bank_auto_assign: true })
  })
})
