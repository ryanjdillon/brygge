import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import AddressCombobox from '@/components/address/AddressCombobox.vue'

const client = { GET: vi.fn() }

vi.mock('@/lib/apiClient', async () => {
  const actual = await vi.importActual<typeof import('@/lib/apiClient')>('@/lib/apiClient')
  return { useApiClient: () => client, unwrap: actual.unwrap, ApiError: actual.ApiError }
})

function ok(data: unknown) {
  return { data, response: new Response(null, { status: 200 }) }
}

// Made-up addresses only.
const A = { id: 'v:4601:90001:12:', label: 'Fiktivvegen 12, 5999 Testby', street: 'Fiktivvegen 12', postal_code: '5999', city: 'Testby', kommunenummer: '4601' }
const B = { id: 'v:4601:90001:12:A', label: 'Fiktivvegen 12A, 5999 Testby', street: 'Fiktivvegen 12A', postal_code: '5999', city: 'Testby', kommunenummer: '4601' }

function mountBox() {
  return mountWithPlugins(AddressCombobox, { props: { id: 'addr' }, attachTo: document.body })
}

// Only the timers are faked: a faked clock would date Vue's listeners later
// than the events the test fires, and Vue drops such events.
beforeEach(() => {
  client.GET.mockReset()
  client.GET.mockResolvedValue(ok({ addresses: [A, B], fallback: false }))
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
})
afterEach(() => {
  vi.useRealTimers()
})

async function type(wrapper: ReturnType<typeof mountBox>, text: string) {
  await wrapper.find('input').setValue(text)
}

async function settle() {
  await vi.advanceTimersByTimeAsync(250)
  await flushPromises()
}

describe('AddressCombobox', () => {
  it('is an ARIA combobox with a 44px listbox and the Kartverket credit', async () => {
    const wrapper = mountBox()
    const input = wrapper.find('input')
    expect(input.attributes('role')).toBe('combobox')
    expect(input.attributes('aria-expanded')).toBe('false')
    expect(input.attributes('aria-controls')).toBe('addr-listbox')
    expect(input.attributes('aria-autocomplete')).toBe('list')
    expect(wrapper.find('[data-testid="address-attribution"]').text()).toBe('join.form.address.attribution')

    await type(wrapper, 'fiktiv')
    await settle()
    expect(input.attributes('aria-expanded')).toBe('true')
    expect(wrapper.find('#addr-listbox').attributes('role')).toBe('listbox')
    const options = wrapper.findAll('[role="option"]')
    expect(options).toHaveLength(2)
    for (const o of options) expect(o.classes()).toContain('min-h-11')
    wrapper.unmount()
  })

  it('debounces: one search for a burst of typing, none under three characters', async () => {
    const wrapper = mountBox()
    await type(wrapper, 'fi')
    await settle()
    expect(client.GET).not.toHaveBeenCalled()

    await type(wrapper, 'fik')
    await vi.advanceTimersByTimeAsync(100)
    await type(wrapper, 'fikt')
    await vi.advanceTimersByTimeAsync(100)
    await type(wrapper, 'fiktiv')
    expect(client.GET).not.toHaveBeenCalled()
    await settle()
    expect(client.GET).toHaveBeenCalledTimes(1)
    expect(client.GET).toHaveBeenCalledWith('/api/v1/join/address-search', { params: { query: { q: 'fiktiv' } } })
    wrapper.unmount()
  })

  it('moves with the arrow keys, picks with Enter and closes with Escape', async () => {
    const wrapper = mountBox()
    const input = wrapper.find('input')
    await type(wrapper, 'fiktiv')
    await settle()

    await input.trigger('keydown', { key: 'ArrowDown' })
    expect(input.attributes('aria-activedescendant')).toBe('addr-option-0')
    expect(wrapper.find('#addr-option-0').attributes('aria-selected')).toBe('true')
    await input.trigger('keydown', { key: 'ArrowDown' })
    expect(input.attributes('aria-activedescendant')).toBe('addr-option-1')
    await input.trigger('keydown', { key: 'ArrowDown' })
    expect(input.attributes('aria-activedescendant')).toBe('addr-option-0')
    await input.trigger('keydown', { key: 'ArrowUp' })
    expect(input.attributes('aria-activedescendant')).toBe('addr-option-1')

    await input.trigger('keydown', { key: 'Escape' })
    expect(input.attributes('aria-expanded')).toBe('false')
    expect(input.attributes('aria-activedescendant')).toBeUndefined()
    expect(wrapper.emitted('select')).toBeUndefined()

    await input.trigger('keydown', { key: 'ArrowDown' })
    await input.trigger('keydown', { key: 'Enter' })
    expect(wrapper.emitted('select')).toEqual([[A]])
    expect(input.attributes('aria-expanded')).toBe('false')

    // Escape on the closed list clears the search.
    await input.trigger('keydown', { key: 'Escape' })
    expect((input.element as HTMLInputElement).value).toBe('')
    wrapper.unmount()
  })

  it('does not reopen the list from a search still waiting when an option is picked', async () => {
    const wrapper = mountBox()
    const input = wrapper.find('input')
    await type(wrapper, 'fiktiv')
    await settle()
    await type(wrapper, 'fiktivv')
    await wrapper.findAll('[role="option"]')[0].trigger('mousedown')
    await settle()
    expect(client.GET).toHaveBeenCalledTimes(1)
    expect(input.attributes('aria-expanded')).toBe('false')
    wrapper.unmount()
  })

  it('keeps the search after a 429, with a hint to wait, and clears it on the next answer', async () => {
    client.GET.mockResolvedValueOnce({ error: { error: 'too many requests' }, response: new Response(null, { status: 429 }) })
    const wrapper = mountBox()
    await type(wrapper, 'fiktiv')
    await settle()
    expect(wrapper.emitted('unavailable')).toBeUndefined()
    expect(wrapper.find('[data-testid="address-search-rate-limited"]').text()).toBe('join.form.address.rateLimited')
    expect(wrapper.find('input').exists()).toBe(true)

    await type(wrapper, 'fiktivv')
    await settle()
    expect(wrapper.find('[data-testid="address-search-rate-limited"]').exists()).toBe(false)
    expect(wrapper.findAll('[role="option"]')).toHaveLength(2)
    wrapper.unmount()
  })

  it('picks an option with the pointer', async () => {
    const wrapper = mountBox()
    await type(wrapper, 'fiktiv')
    await settle()
    await wrapper.findAll('[role="option"]')[1].trigger('mousedown')
    expect(wrapper.emitted('select')).toEqual([[B]])
    wrapper.unmount()
  })

  it('says so when nothing matches', async () => {
    client.GET.mockResolvedValue(ok({ addresses: [], fallback: false }))
    const wrapper = mountBox()
    await type(wrapper, 'ingenstad')
    await settle()
    expect(wrapper.find('[data-testid="address-search-empty"]').text()).toBe('join.form.address.noResults')
    expect(wrapper.emitted('unavailable')).toBeUndefined()
    wrapper.unmount()
  })

  it.each([
    ['the request fails', () => Promise.reject(new TypeError('network'))],
    ['the API answers an error', () => Promise.resolve({ error: { error: 'x' }, response: new Response(null, { status: 500 }) })],
    ['the register is unavailable', () => Promise.resolve(ok({ addresses: [], fallback: true }))],
  ])('reports unavailable when %s', async (_, answer) => {
    client.GET.mockImplementation(answer)
    const wrapper = mountBox()
    await type(wrapper, 'fiktiv')
    await settle()
    expect(wrapper.emitted('unavailable')).toHaveLength(1)
    wrapper.unmount()
  })
})
