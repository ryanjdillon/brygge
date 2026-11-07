import { describe, it, expect, vi, afterEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import EconomySettingsView from '@/views/admin/EconomySettingsView.vue'

const totpAwareFetch = vi.hoisted(() => vi.fn())
const ensureFreshTotp = vi.hoisted(() => vi.fn(async () => true))
vi.mock('@/composables/useFreshTotp', () => ({
  useFreshTotp: () => ({ ensureFreshTotp, totpAwareFetch }),
}))
const canEdit = vi.hoisted(() => ({ value: true }))
vi.mock('@/composables/useCan', () => ({
  useCan: () => ({ can: () => canEdit.value }),
}))
vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ hasRole: () => true }),
}))
vi.mock('lucide-vue-next', () => ({ X: { template: '<span />' } }))

async function mountView(settings: Record<string, unknown> | null) {
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    if (url === '/api/v1/admin/pricing') return { ok: true, json: async () => ({ items: [] }) }
    if (url.startsWith('/api/v1/admin/settings/municipalities/')) {
      return { ok: true, json: async () => ({ code: url.split('/').pop(), name: 'Synthetic Øy', status: 'found' }) }
    }
    if (!settings) return { ok: false, status: 500, statusText: 'error', json: async () => ({}) }
    return { ok: true, json: async () => settings }
  }))
  const wrapper = mountWithPlugins(EconomySettingsView, {
    global: { stubs: { BankAccountsPanel: true, ExemptionsSettingsPanel: true, FileInput: true } },
  })
  await flushPromises()
  return wrapper
}

const OPEN = { join_price_item_id: 'p-join', join_open: true, join_missing: [] }

describe('EconomySettingsView municipality codes', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    totpAwareFetch.mockReset()
    canEdit.value = true
  })

  it('is read only for a board member without the money role', async () => {
    canEdit.value = false
    const wrapper = await mountView({ ...OPEN, municipality_codes: ['9001'] })
    const save = wrapper.get('[data-testid="municipality-save"]').element as HTMLButtonElement
    expect(save.matches(':disabled')).toBe(true)
    expect((wrapper.get('[data-testid="municipality-input"]').element as HTMLInputElement).matches(':disabled')).toBe(true)
  })

  it('warns in the join status that residency cannot be decided without codes', async () => {
    const wrapper = await mountView({ ...OPEN, municipality_codes: [] })
    const join = wrapper.get('[data-testid="join-price"]')
    expect(join.get('[data-testid="join-local-unset"]').text()).toBe('admin.economySettings.joinLocalUnset')
  })

  it('shows the codes with their names and no warning once codes are set', async () => {
    const wrapper = await mountView({ ...OPEN, municipality_codes: ['9001'] })
    expect(wrapper.find('[data-testid="join-local-unset"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="municipality-chip-9001"]').text()).toContain('Synthetic Øy')
  })

  it('clears the warning after the codes are saved', async () => {
    totpAwareFetch.mockResolvedValue({ ok: true, text: async () => '' })
    const wrapper = await mountView({ ...OPEN, municipality_codes: [] })
    await wrapper.get('[data-testid="municipality-input"]').setValue('9001')
    await wrapper.get('[data-testid="municipality-add"]').trigger('click')
    await wrapper.get('[data-testid="municipality-save"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="join-local-unset"]').exists()).toBe(false)
  })

  it('shows neither the editor nor the warning when the settings did not load', async () => {
    const wrapper = await mountView(null)
    expect(wrapper.find('[data-testid="municipality-codes"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="join-local-unset"]').exists()).toBe(false)
  })
})
