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
const roles = vi.hoisted(() => ({ list: ['admin'] as string[] }))
vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ hasRole: (r: string) => roles.list.includes(r) }),
}))

const ITEMS = [
  { id: 'p-slip', name: 'Synthetic slip fee', category: 'slip_fee', amount: 900, is_active: true, pricing_kind: 'flat' },
  { id: 'p-join', name: 'Synthetic membership', category: 'membership', amount: 500, is_active: true, pricing_kind: 'flat' },
  { id: 'p-old', name: 'Synthetic archived', category: 'membership', amount: 400, is_active: false, pricing_kind: 'flat' },
  { id: 'p-tier', name: 'Synthetic tiered', category: 'membership', amount: 0, is_active: true, pricing_kind: 'tiered' },
]

async function mountView(settings: Record<string, unknown>, refreshed?: Record<string, unknown>, pricingOk = true) {
  let settingsReads = 0
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    if (url === '/api/v1/admin/pricing') {
      return pricingOk ? { ok: true, json: async () => ({ items: ITEMS }) } : { ok: false, status: 500, json: async () => ({}) }
    }
    settingsReads++
    return { ok: true, json: async () => (settingsReads > 1 && refreshed ? refreshed : settings) }
  }))
  const wrapper = mountWithPlugins(EconomySettingsView, {
    global: { stubs: { BankAccountsPanel: true, ExemptionsSettingsPanel: true, FileInput: true } },
  })
  await flushPromises()
  const box = wrapper.get('[data-testid="join-price"]')
  return { wrapper, box, select: box.get('[data-testid="join-price-select"]'), save: box.get('[data-testid="join-price-save"]') }
}

describe('EconomySettingsView joining price', () => {
  afterEach(() => {
    roles.list = ['admin']
    vi.unstubAllGlobals()
    totpAwareFetch.mockReset()
    ensureFreshTotp.mockReset()
    ensureFreshTotp.mockImplementation(async () => true)
  })

  it('offers "none" and the active flat items, membership first', async () => {
    const { select } = await mountView({ join_price_item_id: '', join_open: false, join_missing: ['price_item_unset'] })
    const values = select.findAll('option').map((o) => o.attributes('value'))
    expect(values).toEqual(['', 'p-join', 'p-slip'])
    expect(select.findAll('option')[0].text()).toBe('admin.economySettings.joinPriceNone')
  })

  it('keeps a stored item that is no longer usable visible and marked', async () => {
    const { select } = await mountView({ join_price_item_id: 'p-old', join_open: false, join_missing: ['price_item_inactive'] })
    expect((select.element as HTMLSelectElement).value).toBe('p-old')
    expect(select.findAll('option')[1].text()).toBe('admin.economySettings.joinPriceUnusable')
  })

  it('says joining is open when nothing is missing', async () => {
    const { box } = await mountView({ join_price_item_id: 'p-join', join_open: true, join_missing: [] })
    expect(box.get('[data-testid="join-status-open"]').text()).toBe('admin.economySettings.joinStatusOpen')
    expect(box.find('[data-testid="join-status-closed"]').exists()).toBe(false)
  })

  it('lists what is missing in words, with links to fix it', async () => {
    const { box } = await mountView({
      join_price_item_id: '',
      join_open: false,
      join_missing: ['price_item_unset', 'statutes'],
    })
    const closed = box.get('[data-testid="join-status-closed"]')
    const items = closed.findAll('li').map((li) => li.text())
    expect(items).toEqual(['admin.economySettings.joinMissingPriceUnset', 'admin.economySettings.joinMissingDocument'])
    const links = closed.findAll('a').map((a) => a.attributes('href'))
    expect(links).toEqual(['/admin/pricing', '/admin/gdpr'])
  })

  it('asks a treasurer to get an admin to publish, without linking the admin-only page', async () => {
    roles.list = ['treasurer']
    const { box } = await mountView({ join_price_item_id: '', join_open: false, join_missing: ['price_item_unset', 'privacy'] })
    const closed = box.get('[data-testid="join-status-closed"]')
    expect(closed.findAll('a').map((a) => a.attributes('href'))).toEqual(['/admin/pricing'])
    expect(closed.get('[data-testid="join-ask-admin"]').text()).toBe('admin.economySettings.joinAskAdmin')
  })

  it('does not mark the stored item unusable when the price list fails to load', async () => {
    const { box, select } = await mountView(
      { join_price_item_id: 'p-join', join_open: true, join_missing: [] }, undefined, false,
    )
    expect((select.element as HTMLSelectElement).value).toBe('p-join')
    const labels = select.findAll('option').map((o) => o.text())
    expect(labels).toEqual(['admin.economySettings.joinPriceNone', 'admin.economySettings.joinPriceCurrent'])
    expect(box.get('[data-testid="join-price-list-failed"]').text()).toBe('admin.economySettings.joinPriceListFailed')
  })

  it('links only to the legal documents when only documents are missing', async () => {
    const { box } = await mountView({ join_price_item_id: 'p-join', join_open: false, join_missing: ['privacy'] })
    const links = box.get('[data-testid="join-status-closed"]').findAll('a').map((a) => a.attributes('href'))
    expect(links).toEqual(['/admin/gdpr'])
  })

  it('saves the item after the fresh TOTP check and re-reads the status', async () => {
    totpAwareFetch.mockResolvedValue({ ok: true, text: async () => '' })
    const { box, select, save } = await mountView(
      { join_price_item_id: '', join_open: false, join_missing: ['price_item_unset'] },
      { join_price_item_id: 'p-join', join_open: true, join_missing: [] },
    )

    await select.setValue('p-join')
    await save.trigger('click')
    await flushPromises()

    expect(ensureFreshTotp).toHaveBeenCalledTimes(1)
    expect(ensureFreshTotp.mock.invocationCallOrder[0]).toBeLessThan(totpAwareFetch.mock.invocationCallOrder[0])
    const [url, init] = totpAwareFetch.mock.calls[0]
    expect(url).toBe('/api/v1/admin/settings/site')
    expect(init.method).toBe('PATCH')
    expect(JSON.parse(String(init.body))).toEqual({ join_price_item_id: 'p-join' })
    expect(box.text()).toContain('common.saved')
    expect(box.find('[data-testid="join-status-open"]').exists()).toBe(true)
  })

  it('sends an empty value to close joining', async () => {
    totpAwareFetch.mockResolvedValue({ ok: true, text: async () => '' })
    const { select, save } = await mountView({ join_price_item_id: 'p-join', join_open: true, join_missing: [] })

    await select.setValue('')
    await save.trigger('click')
    await flushPromises()

    expect(JSON.parse(String(totpAwareFetch.mock.calls[0][1].body))).toEqual({ join_price_item_id: '' })
  })

  it('does not save when the fresh TOTP check is declined', async () => {
    ensureFreshTotp.mockResolvedValue(false)
    const { select, save } = await mountView({ join_price_item_id: '', join_open: false, join_missing: ['price_item_unset'] })

    await select.setValue('p-join')
    await save.trigger('click')
    await flushPromises()

    expect(totpAwareFetch).not.toHaveBeenCalled()
  })

  it('words the typed 400 refusal next to the field', async () => {
    totpAwareFetch.mockResolvedValue({
      ok: false,
      status: 400,
      text: async () => JSON.stringify({ error: 'refused', code: 'invalid_join_price_item' }),
    })
    const { box, select, save } = await mountView({ join_price_item_id: '', join_open: false, join_missing: ['price_item_unset'] })

    await select.setValue('p-slip')
    await save.trigger('click')
    await flushPromises()

    expect(box.get('[role="alert"]').text()).toBe('admin.economySettings.joinPriceInvalid')
    expect(box.text()).not.toContain('common.saved')
  })
})
