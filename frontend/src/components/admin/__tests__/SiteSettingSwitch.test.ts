import { describe, it, expect, vi, afterEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import SiteSettingSwitch from '@/components/admin/SiteSettingSwitch.vue'

const totpAwareFetch = vi.hoisted(() => vi.fn())
const ensureFreshTotp = vi.hoisted(() => vi.fn(async () => true))
vi.mock('@/composables/useFreshTotp', () => ({
  useFreshTotp: () => ({ ensureFreshTotp, totpAwareFetch }),
}))

function mountSwitch(props: { modelValue?: boolean; ready?: boolean } = {}) {
  const wrapper = mountWithPlugins(SiteSettingSwitch, {
    props: {
      field: 'automatic_booking',
      legend: 'Legend',
      label: 'Label',
      hint: 'Hint text',
      ready: props.ready ?? true,
      modelValue: props.modelValue ?? false,
      'onUpdate:modelValue': (v: boolean) => wrapper.setProps({ modelValue: v }),
    },
    attrs: { 'data-testid': 'automatic-booking' },
  })
  const checkbox = () => wrapper.get('input[type="checkbox"]')
  return { wrapper, checkbox, save: wrapper.get('button') }
}

function checked(el: { element: Element }) {
  return (el.element as HTMLInputElement).checked
}

// BRY-568: one on/off club setting with its own Save, shared by the bank
// auto-assign rule and the automatic booking master switch.
describe('SiteSettingSwitch', () => {
  afterEach(() => {
    totpAwareFetch.mockReset()
    ensureFreshTotp.mockReset()
    ensureFreshTotp.mockImplementation(async () => true)
  })

  it('shows the value, legend, label and hint, and passes data-testid to its fieldset', () => {
    const { wrapper, checkbox } = mountSwitch({ modelValue: true })
    expect(checked(checkbox())).toBe(true)
    expect(wrapper.element.tagName).toBe('FIELDSET')
    expect(wrapper.attributes('data-testid')).toBe('automatic-booking')
    expect(wrapper.get('legend').text()).toBe('Legend')
    expect(wrapper.text()).toContain('Label')
    const hint = wrapper.get('p')
    expect(hint.text()).toBe('Hint text')
    expect(checkbox().attributes('aria-describedby')).toBe(hint.attributes('id'))
  })

  it('asks for a fresh TOTP first, then PATCHes only its field', async () => {
    totpAwareFetch.mockResolvedValue({ ok: true, text: async () => '' })
    const { wrapper, checkbox, save } = mountSwitch()

    await checkbox().setValue(true)
    await save.trigger('click')
    await flushPromises()

    expect(ensureFreshTotp).toHaveBeenCalledTimes(1)
    expect(ensureFreshTotp.mock.invocationCallOrder[0]).toBeLessThan(totpAwareFetch.mock.invocationCallOrder[0])
    const [url, init] = totpAwareFetch.mock.calls[0]
    expect(url).toBe('/api/v1/admin/settings/site')
    expect(init.method).toBe('PATCH')
    expect(JSON.parse(String(init.body))).toEqual({ automatic_booking: true })
    expect(wrapper.get('[role="status"]').text()).toBe('common.saved')
    expect(checked(checkbox())).toBe(true)
    expect(wrapper.emitted('error')).toEqual([[null]])

    await checkbox().setValue(false)
    expect(wrapper.find('[role="status"]').exists()).toBe(false)
  })

  it('puts the box back and sends nothing when the fresh TOTP check is declined', async () => {
    ensureFreshTotp.mockResolvedValue(false)
    const { wrapper, checkbox, save } = mountSwitch({ modelValue: false })

    await checkbox().setValue(true)
    await save.trigger('click')
    await flushPromises()

    expect(totpAwareFetch).not.toHaveBeenCalled()
    expect(checked(checkbox())).toBe(false)
    expect(wrapper.find('[role="status"]').exists()).toBe(false)
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
  })

  it('puts the box back to the stored value on a failed save and shows the error in the box', async () => {
    totpAwareFetch.mockResolvedValue({ ok: false, status: 500, text: async () => 'boom' })
    const { wrapper, checkbox, save } = mountSwitch({ modelValue: true })

    await checkbox().setValue(false)
    await save.trigger('click')
    await flushPromises()

    expect(checked(checkbox())).toBe(true)
    const alert = wrapper.get('[role="alert"]')
    expect(alert.text()).toBe('500 boom')
    expect(checkbox().attributes('aria-describedby')).toBe(
      `${wrapper.get('p').attributes('id')} ${alert.attributes('id')}`,
    )
    expect(checkbox().attributes('aria-invalid')).toBe('true')
    expect(wrapper.emitted('error')).toEqual([[null], ['500 boom']])
    expect(wrapper.find('[role="status"]').exists()).toBe(false)

    await checkbox().setValue(false)
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
  })

  it('reverts to the last saved value, not the first one', async () => {
    totpAwareFetch.mockResolvedValueOnce({ ok: true, text: async () => '' })
    totpAwareFetch.mockResolvedValueOnce({ ok: false, status: 500, text: async () => 'boom' })
    const { checkbox, save } = mountSwitch({ modelValue: false })

    await checkbox().setValue(true)
    await save.trigger('click')
    await flushPromises()
    await checkbox().setValue(false)
    await save.trigger('click')
    await flushPromises()

    expect(checked(checkbox())).toBe(true)
  })

  it('takes the stored value when the settings finish loading', async () => {
    totpAwareFetch.mockResolvedValue({ ok: false, status: 500, text: async () => 'boom' })
    const { wrapper, checkbox, save } = mountSwitch({ modelValue: false, ready: false })

    await wrapper.setProps({ modelValue: true, ready: true })
    await checkbox().setValue(false)
    await save.trigger('click')
    await flushPromises()

    expect(checked(checkbox())).toBe(true)
  })

  it('starts one save for a double click', async () => {
    let allow!: (ok: boolean) => void
    ensureFreshTotp.mockImplementation(() => new Promise<boolean>((resolve) => { allow = resolve }))
    totpAwareFetch.mockResolvedValue({ ok: true, text: async () => '' })
    const { save } = mountSwitch()

    await save.trigger('click')
    await save.trigger('click')
    allow(true)
    await flushPromises()

    expect(ensureFreshTotp).toHaveBeenCalledTimes(1)
    expect(totpAwareFetch).toHaveBeenCalledTimes(1)
  })

  it('keeps Save off until the stored value has loaded', async () => {
    const { save } = mountSwitch({ ready: false })
    expect(save.element.matches(':disabled')).toBe(true)
    await save.trigger('click')
    await flushPromises()
    expect(ensureFreshTotp).not.toHaveBeenCalled()
  })

  it('keeps its controls at least 44px tall and wrapping on a phone', () => {
    const { wrapper, checkbox, save } = mountSwitch()
    expect(checkbox().element.closest('label')!.classList).toContain('min-h-[44px]')
    expect(save.classes()).toContain('min-h-[44px]')
    expect(wrapper.get('div').classes()).toEqual(expect.arrayContaining(['flex', 'flex-wrap']))
  })
})
