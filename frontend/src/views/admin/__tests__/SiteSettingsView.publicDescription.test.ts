import { describe, it, expect, vi, afterEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import SiteSettingsView from '@/views/admin/SiteSettingsView.vue'
import { useClubStore } from '@/stores/club'

const totpAwareFetch = vi.hoisted(() => vi.fn())
const ensureFreshTotp = vi.hoisted(() => vi.fn(async () => true))
vi.mock('@/composables/useFreshTotp', () => ({
  useFreshTotp: () => ({ ensureFreshTotp, totpAwareFetch }),
}))

async function mountView(settings: Record<string, unknown>) {
  vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, json: async () => settings })))
  const wrapper = mountWithPlugins(SiteSettingsView, { global: { stubs: { FileInput: true } } })
  await flushPromises()
  const field = wrapper.get('[data-testid="public-description"]')
  return { wrapper, input: field.get('input') }
}

describe('SiteSettingsView public description', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    totpAwareFetch.mockReset()
    ensureFreshTotp.mockReset()
    ensureFreshTotp.mockImplementation(async () => true)
  })

  it('sits with the club name and explains the fallback', async () => {
    const { wrapper, input } = await mountView({ name: 'Synthetic Club', public_description: 'Boats and coffee' })
    expect((input.element as HTMLInputElement).value).toBe('Boats and coffee')
    expect(input.attributes('maxlength')).toBe('200')
    expect(input.attributes('placeholder')).toBe('home.tagline')
    expect(wrapper.get('label[for="public-description"]').text()).toContain('admin.siteSettings.publicDescription')
    expect(wrapper.text()).toContain('admin.siteSettings.publicDescriptionHint')
    // In the identity group, right after the (read-only) club name.
    const identity = wrapper.findAll('fieldset')[0]
    expect(identity.find('#public-description').exists()).toBe(true)
  })

  it('is empty when the club has none', async () => {
    const { input } = await mountView({ name: 'Synthetic Club' })
    expect((input.element as HTMLInputElement).value).toBe('')
  })

  it('saves the trimmed text with the form, after the fresh TOTP check', async () => {
    totpAwareFetch.mockResolvedValue({ ok: true, text: async () => '' })
    const { wrapper, input } = await mountView({ name: 'Synthetic Club', public_description: '' })

    await input.setValue('  A small club by the fjord  ')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(ensureFreshTotp).toHaveBeenCalledTimes(1)
    const [url, init] = totpAwareFetch.mock.calls[0]
    expect(url).toBe('/api/v1/admin/settings/site')
    expect(init.method).toBe('PATCH')
    expect(JSON.parse(String(init.body)).public_description).toBe('A small club by the fjord')
    expect((input.element as HTMLInputElement).value).toBe('A small club by the fjord')
    // The landing page's store gets the saved text without a reload.
    expect(useClubStore().publicDescription).toBe('A small club by the fjord')
  })

  it('leaves the club store alone when the save is refused', async () => {
    totpAwareFetch.mockResolvedValue({
      ok: false,
      status: 400,
      text: async () => JSON.stringify({ error: 'too long', code: 'invalid_public_description' }),
    })
    const { wrapper, input } = await mountView({ name: 'Synthetic Club', public_description: 'Old text' })
    useClubStore().publicDescription = 'Old text'

    await input.setValue('New text')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(useClubStore().publicDescription).toBe('Old text')
  })

  it('words the refusal of a too-long description', async () => {
    totpAwareFetch.mockResolvedValue({
      ok: false,
      status: 400,
      text: async () => JSON.stringify({ error: 'too long', code: 'invalid_public_description' }),
    })
    const { wrapper } = await mountView({ name: 'Synthetic Club' })

    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(wrapper.text()).toContain('admin.siteSettings.publicDescriptionInvalid')
  })
})
