import { describe, it, expect, vi, afterEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import MunicipalityCodesSetting from '@/components/admin/MunicipalityCodesSetting.vue'

const totpAwareFetch = vi.hoisted(() => vi.fn())
const ensureFreshTotp = vi.hoisted(() => vi.fn(async () => true))
vi.mock('@/composables/useFreshTotp', () => ({
  useFreshTotp: () => ({ ensureFreshTotp, totpAwareFetch }),
}))
vi.mock('lucide-vue-next', () => ({ X: { template: '<span />' } }))

// Synthetic codes and names; the lookup answers as the server would.
const LOOKUP: Record<string, { status: string; name?: string }> = {
  '9001': { status: 'found', name: 'Synthetic Øy' },
  '9002': { status: 'found', name: 'Synthetic Vik' },
  '9003': { status: 'unknown' },
}

function stubLookup() {
  const fetchMock = vi.fn(async (url: string) => {
    const code = url.split('/').pop() as string
    if (code === '9500') return { ok: false, status: 500, json: async () => ({}) }
    const a = LOOKUP[code] ?? { status: 'unavailable' }
    return { ok: true, json: async () => ({ code, name: a.name ?? '', status: a.status }) }
  })
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

async function mountSetting(codes: string[]) {
  const fetchMock = stubLookup()
  const wrapper = mountWithPlugins(MunicipalityCodesSetting, { props: { codes } })
  await flushPromises()
  return { wrapper, fetchMock }
}

function chipCodes(wrapper: Awaited<ReturnType<typeof mountSetting>>['wrapper']) {
  return wrapper.findAll('[data-testid^="municipality-chip-"]').map((c) => c.attributes('data-testid')!.slice('municipality-chip-'.length))
}

describe('MunicipalityCodesSetting', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    totpAwareFetch.mockReset()
    ensureFreshTotp.mockReset()
    ensureFreshTotp.mockImplementation(async () => true)
  })

  it('shows each code with its name, marks an unknown one, and shows a failed lookup as the code alone', async () => {
    const { wrapper } = await mountSetting(['9001', '9003', '9500'])
    expect(wrapper.get('[data-testid="municipality-chip-9001"]').text()).toContain('Synthetic Øy')
    const unknown = wrapper.get('[data-testid="municipality-chip-9003"]')
    expect(unknown.find('[data-testid="municipality-unknown"]').exists()).toBe(true)
    const failed = wrapper.get('[data-testid="municipality-chip-9500"]')
    expect(failed.find('[data-testid="municipality-name"]').exists()).toBe(false)
    expect(failed.find('[data-testid="municipality-unknown"]').exists()).toBe(false)
    expect(failed.text()).toContain('9500')
  })

  it('says when no codes are set', async () => {
    const { wrapper } = await mountSetting([])
    expect(wrapper.get('[data-testid="municipality-none"]').text()).toBe('admin.economySettings.municipalityCodesNone')
  })

  it('adds a four-digit code once, with its name, and refuses anything else', async () => {
    const { wrapper } = await mountSetting(['9001'])
    const input = wrapper.get('[data-testid="municipality-input"]')

    await input.setValue(' 9002 ')
    await wrapper.get('[data-testid="municipality-add"]').trigger('click')
    await flushPromises()
    expect(chipCodes(wrapper)).toEqual(['9001', '9002'])
    expect(wrapper.get('[data-testid="municipality-chip-9002"]').text()).toContain('Synthetic Vik')
    expect((input.element as HTMLInputElement).value).toBe('')

    await input.setValue('9001')
    await input.trigger('keydown', { key: 'Enter' })
    expect(chipCodes(wrapper)).toEqual(['9001', '9002'])

    await input.setValue('90a')
    await wrapper.get('[data-testid="municipality-add"]').trigger('click')
    expect(chipCodes(wrapper)).toEqual(['9001', '9002'])
    expect(wrapper.get('[data-testid="municipality-input-error"]').text()).toBe('admin.economySettings.municipalityCodesBadInput')
  })

  it('stops at twenty codes', async () => {
    const twenty = Array.from({ length: 20 }, (_, i) => String(9100 + i))
    const { wrapper } = await mountSetting(twenty)
    await wrapper.get('[data-testid="municipality-input"]').setValue('9999')
    await wrapper.get('[data-testid="municipality-add"]').trigger('click')
    expect(chipCodes(wrapper)).toHaveLength(20)
    expect(wrapper.get('[data-testid="municipality-input-error"]').text()).toBe('admin.economySettings.municipalityCodesMax')
  })

  it('removes a chip', async () => {
    const { wrapper } = await mountSetting(['9001', '9002'])
    await wrapper.get('[data-testid="municipality-remove-9001"]').trigger('click')
    expect(chipCodes(wrapper)).toEqual(['9002'])
  })

  it('asks for a fresh TOTP at the click, before saving, and saves the list', async () => {
    totpAwareFetch.mockResolvedValue({ ok: true, text: async () => '' })
    const { wrapper } = await mountSetting(['9001', '9002'])
    await wrapper.get('[data-testid="municipality-remove-9002"]').trigger('click')
    await wrapper.get('[data-testid="municipality-save"]').trigger('click')
    await flushPromises()

    expect(ensureFreshTotp).toHaveBeenCalledTimes(1)
    expect(ensureFreshTotp.mock.invocationCallOrder[0]).toBeLessThan(totpAwareFetch.mock.invocationCallOrder[0])
    const [url, init] = totpAwareFetch.mock.calls[0]
    expect(url).toBe('/api/v1/admin/settings/site')
    expect(init.method).toBe('PATCH')
    expect(JSON.parse(init.body)).toEqual({ municipality_codes: ['9001'] })
    expect(wrapper.emitted('saved')).toEqual([[['9001']]])
  })

  it('saves a valid code still in the input along with the list', async () => {
    totpAwareFetch.mockResolvedValue({ ok: true, text: async () => '' })
    const { wrapper } = await mountSetting(['9001'])
    await wrapper.get('[data-testid="municipality-input"]').setValue('9002')
    await wrapper.get('[data-testid="municipality-save"]').trigger('click')
    await flushPromises()
    expect(JSON.parse(totpAwareFetch.mock.calls[0][1].body)).toEqual({ municipality_codes: ['9001', '9002'] })
    expect(chipCodes(wrapper)).toEqual(['9001', '9002'])
  })

  it('blocks the save, before the TOTP prompt, while the input holds an invalid code', async () => {
    const { wrapper } = await mountSetting(['9001'])
    const input = wrapper.get('[data-testid="municipality-input"]')
    await input.setValue('90a')
    await wrapper.get('[data-testid="municipality-save"]').trigger('click')
    await flushPromises()
    expect(ensureFreshTotp).not.toHaveBeenCalled()
    expect(totpAwareFetch).not.toHaveBeenCalled()
    const error = wrapper.get('[data-testid="municipality-input-error"]')
    expect(error.text()).toBe('admin.economySettings.municipalityCodesBadInput')
    expect(error.attributes('role')).toBe('alert')
    expect(input.attributes('aria-invalid')).toBe('true')
    expect(input.attributes('aria-describedby')).toBe('municipality-codes-hint municipality-input-error')
  })

  it('keeps unsaved edits when the parent reloads, and follows the reload when unedited', async () => {
    const { wrapper } = await mountSetting(['9001'])
    await wrapper.setProps({ codes: ['9002'] })
    expect(chipCodes(wrapper)).toEqual(['9002'])

    await wrapper.get('[data-testid="municipality-input"]').setValue('9003')
    await wrapper.get('[data-testid="municipality-add"]').trigger('click')
    await wrapper.setProps({ codes: ['9001'] })
    expect(chipCodes(wrapper)).toEqual(['9002', '9003'])
  })

  it('announces the name of a code just added', async () => {
    const { wrapper } = await mountSetting([])
    await wrapper.get('[data-testid="municipality-input"]').setValue('9001')
    await wrapper.get('[data-testid="municipality-add"]').trigger('click')
    await flushPromises()
    const live = wrapper.get('[data-testid="municipality-announce"]')
    expect(live.attributes('aria-live')).toBe('polite')
    expect(live.text()).toBe('9001 Synthetic Øy')
  })

  it('saves nothing when the TOTP prompt is cancelled', async () => {
    ensureFreshTotp.mockImplementation(async () => false)
    const { wrapper } = await mountSetting(['9001'])
    await wrapper.get('[data-testid="municipality-save"]').trigger('click')
    await flushPromises()
    expect(totpAwareFetch).not.toHaveBeenCalled()
    expect(wrapper.emitted('saved')).toBeUndefined()
  })

  it('shows the refusal in words', async () => {
    totpAwareFetch.mockResolvedValue({
      ok: false,
      status: 400,
      text: async () => JSON.stringify({ error: 'x', code: 'invalid_municipality_codes' }),
    })
    const { wrapper } = await mountSetting(['9001'])
    await wrapper.get('[data-testid="municipality-save"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('admin.economySettings.municipalityCodesInvalid')
    expect(wrapper.emitted('saved')).toBeUndefined()
  })
})
