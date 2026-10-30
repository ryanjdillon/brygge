import { describe, it, expect, vi, afterEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import EconomySettingsView from '@/views/admin/EconomySettingsView.vue'

const totpAwareFetch = vi.hoisted(() => vi.fn())
const ensureFreshTotp = vi.hoisted(() => vi.fn(async () => true))
vi.mock('@/composables/useFreshTotp', () => ({
  useFreshTotp: () => ({ ensureFreshTotp, totpAwareFetch }),
}))
vi.mock('@/composables/useCan', () => ({
  useCan: () => ({ can: () => true }),
}))

const STORED = 'https://portal.vippsmobilepay.com/100200/home'

async function mountView(settings: Record<string, unknown>) {
  vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, json: async () => settings })))
  const wrapper = mountWithPlugins(EconomySettingsView, {
    global: { stubs: { BankAccountsPanel: true, ExemptionsSettingsPanel: true, FileInput: true } },
  })
  await flushPromises()
  const box = wrapper.get('[data-testid="vipps-portal-url"]')
  return { wrapper, box, input: box.get('input'), save: box.get('button') }
}

describe('EconomySettingsView Vipps portal address', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    totpAwareFetch.mockReset()
    ensureFreshTotp.mockReset()
    ensureFreshTotp.mockImplementation(async () => true)
  })

  it('shows the stored address and the helper text', async () => {
    const { box, input } = await mountView({ vipps_portal_url: STORED })
    expect((input.element as HTMLInputElement).value).toBe(STORED)
    expect(box.text()).toContain('admin.economySettings.vippsPortalUrlHint')
    expect(input.attributes('aria-describedby')).toBe('vipps-portal-url-hint')
  })

  it('is empty when no address is stored', async () => {
    const { input } = await mountView({ vipps_portal_url: '' })
    expect((input.element as HTMLInputElement).value).toBe('')
  })

  it('saves the address on its own, after the fresh TOTP check', async () => {
    totpAwareFetch.mockResolvedValue({ ok: true, text: async () => '' })
    const { box, input, save } = await mountView({ vipps_portal_url: '' })

    await input.setValue(STORED)
    await save.trigger('click')
    await flushPromises()

    expect(ensureFreshTotp).toHaveBeenCalledTimes(1)
    expect(ensureFreshTotp.mock.invocationCallOrder[0]).toBeLessThan(totpAwareFetch.mock.invocationCallOrder[0])
    const [url, init] = totpAwareFetch.mock.calls[0]
    expect(url).toBe('/api/v1/admin/settings/site')
    expect(init.method).toBe('PATCH')
    expect(JSON.parse(String(init.body))).toEqual({ vipps_portal_url: STORED })
    expect(box.text()).toContain('common.saved')
  })

  it('does not save when the fresh TOTP check is declined', async () => {
    ensureFreshTotp.mockResolvedValue(false)
    const { box, input, save } = await mountView({ vipps_portal_url: '' })

    await input.setValue(STORED)
    await save.trigger('click')
    await flushPromises()

    expect(ensureFreshTotp).toHaveBeenCalledTimes(1)
    expect(totpAwareFetch).not.toHaveBeenCalled()
    expect(box.text()).not.toContain('common.saved')
  })

  it('sends an empty value to clear the link', async () => {
    totpAwareFetch.mockResolvedValue({ ok: true, text: async () => '' })
    const { input, save } = await mountView({ vipps_portal_url: STORED })

    await input.setValue('')
    await save.trigger('click')
    await flushPromises()

    expect(JSON.parse(String(totpAwareFetch.mock.calls[0][1].body))).toEqual({ vipps_portal_url: '' })
  })

  it('words the typed 400 refusal next to the field', async () => {
    totpAwareFetch.mockResolvedValue({
      ok: false,
      status: 400,
      text: async () => JSON.stringify({ error: 'refused', code: 'invalid_vipps_portal_url' }),
    })
    const { wrapper, box, input, save } = await mountView({ vipps_portal_url: '' })

    await input.setValue('https://portal.example.com/1/home')
    await save.trigger('click')
    await flushPromises()

    const alert = box.get('[role="alert"]')
    expect(alert.text()).toBe('admin.economySettings.vippsPortalUrlInvalid')
    expect(alert.attributes('id')).toBe('vipps-portal-url-error')
    expect(input.attributes('aria-invalid')).toBe('true')
    expect(input.attributes('aria-describedby')).toBe('vipps-portal-url-hint vipps-portal-url-error')
    expect(box.text()).not.toContain('common.saved')
    // The page-level banner is for failures that are not the field's.
    expect(wrapper.text()).not.toContain('invalid_vipps_portal_url')

    await input.setValue(STORED)
    expect(box.find('[role="alert"]').exists()).toBe(false)
    expect(input.attributes('aria-describedby')).toBe('vipps-portal-url-hint')
  })

  it('shows other failures in the page banner', async () => {
    totpAwareFetch.mockResolvedValue({ ok: false, status: 500, text: async () => 'boom' })
    const { wrapper, box, input, save } = await mountView({ vipps_portal_url: '' })

    await input.setValue(STORED)
    await save.trigger('click')
    await flushPromises()

    expect(box.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('500 boom')
  })

  it('keeps the field and its button at least 44px tall and stacked on a phone', async () => {
    const { box, input, save } = await mountView({ vipps_portal_url: '' })
    expect(input.classes()).toContain('min-h-[44px]')
    expect(save.classes()).toContain('min-h-[44px]')
    expect(box.get('div').classes()).toEqual(expect.arrayContaining(['flex-col', 'md:flex-row']))
  })
})
