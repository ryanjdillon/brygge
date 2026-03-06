import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mountWithPlugins } from '@/test/test-utils'
import { flushPromises } from '@vue/test-utils'
import JoinView from '@/views/JoinView.vue'

describe('JoinView', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
  })

  it('renders registration form fields', () => {
    const wrapper = mountWithPlugins(JoinView)

    expect(wrapper.find('#join-name').exists()).toBe(true)
    expect(wrapper.find('#join-email').exists()).toBe(true)
    expect(wrapper.find('#join-phone').exists()).toBe(true)
  })

  it('renders boat details section', () => {
    const wrapper = mountWithPlugins(JoinView)

    expect(wrapper.find('fieldset').exists()).toBe(true)
    expect(wrapper.text()).toContain('join.boatDetails')
    expect(wrapper.find('#join-boat-name').exists()).toBe(true)
    expect(wrapper.find('#join-boat-type').exists()).toBe(true)
    expect(wrapper.find('#join-boat-length').exists()).toBe(true)
    expect(wrapper.find('#join-boat-beam').exists()).toBe(true)
    expect(wrapper.find('#join-boat-draft').exists()).toBe(true)
  })

  it('renders title and subtitle', () => {
    const wrapper = mountWithPlugins(JoinView)
    expect(wrapper.find('h1').text()).toBe('join.title')
    expect(wrapper.text()).toContain('join.subtitle')
  })

  it('form submission with valid data calls API', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response(JSON.stringify({ queuePosition: 5 }), { status: 200 }),
    )

    const wrapper = mountWithPlugins(JoinView)

    await wrapper.find('#join-name').setValue('Test User')
    await wrapper.find('#join-email').setValue('test@example.com')
    await wrapper.find('#join-phone').setValue('+4799999999')
    await wrapper.find('#join-boat-name').setValue('Sjark')
    await wrapper.find('#join-boat-type').setValue('Sailboat')
    await wrapper.find('#join-boat-length').setValue('10')
    await wrapper.find('#join-boat-beam').setValue('3.5')
    await wrapper.find('#join-boat-draft').setValue('1.8')

    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(fetchSpy).toHaveBeenCalledWith('/api/v1/join', expect.objectContaining({
      method: 'POST',
    }))

    expect(wrapper.text()).toContain('join.submitted')
  })

  it('shows error on failed submission', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValueOnce(new Error('Network error'))

    const wrapper = mountWithPlugins(JoinView)

    await wrapper.find('#join-name').setValue('Test User')
    await wrapper.find('#join-email').setValue('test@example.com')
    await wrapper.find('#join-phone').setValue('+4799999999')

    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(wrapper.text()).toContain('common.error')
  })
})
