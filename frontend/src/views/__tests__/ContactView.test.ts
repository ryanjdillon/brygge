import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mountWithPlugins } from '@/test/test-utils'
import { flushPromises } from '@vue/test-utils'
import ContactView from '@/views/ContactView.vue'

vi.mock('lucide-vue-next', () => ({
  MapPin: { template: '<span data-icon="mappin" />' },
  Phone: { template: '<span data-icon="phone" />' },
  Radio: { template: '<span data-icon="radio" />' },
  Mail: { template: '<span data-icon="mail" />' },
  MessageCircle: { template: '<span data-icon="messagecircle" />' },
}))

describe('ContactView', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
  })

  it('renders contact form with all fields', () => {
    const wrapper = mountWithPlugins(ContactView)

    expect(wrapper.find('#contact-name').exists()).toBe(true)
    expect(wrapper.find('#contact-email').exists()).toBe(true)
    expect(wrapper.find('#contact-subject').exists()).toBe(true)
    expect(wrapper.find('#contact-message').exists()).toBe(true)
  })

  it('renders contact info section', () => {
    const wrapper = mountWithPlugins(ContactView)
    expect(wrapper.text()).toContain('contact.title')
    expect(wrapper.text()).toContain('contact.address')
    expect(wrapper.text()).toContain('contact.phone')
    expect(wrapper.text()).toContain('contact.email')
  })

  it('form fields have required attribute', () => {
    const wrapper = mountWithPlugins(ContactView)

    expect(wrapper.find('#contact-name').attributes('required')).toBeDefined()
    expect(wrapper.find('#contact-email').attributes('required')).toBeDefined()
    expect(wrapper.find('#contact-subject').attributes('required')).toBeDefined()
    expect(wrapper.find('#contact-message').attributes('required')).toBeDefined()
  })

  it('successful form submission calls API', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response(JSON.stringify({ ok: true }), { status: 200 }),
    )

    const wrapper = mountWithPlugins(ContactView)

    await wrapper.find('#contact-name').setValue('John Doe')
    await wrapper.find('#contact-email').setValue('john@example.com')
    await wrapper.find('#contact-subject').setValue('Test Subject')
    await wrapper.find('#contact-message').setValue('Test message body')

    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(fetchSpy).toHaveBeenCalledWith('/api/v1/contact', expect.objectContaining({
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        name: 'John Doe',
        email: 'john@example.com',
        subject: 'Test Subject',
        message: 'Test message body',
      }),
    }))

    expect(wrapper.text()).toContain('contact.sent')
  })

  it('shows error message on failed submission', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValueOnce(new Error('Network error'))

    const wrapper = mountWithPlugins(ContactView)

    await wrapper.find('#contact-name').setValue('John')
    await wrapper.find('#contact-email').setValue('john@example.com')
    await wrapper.find('#contact-subject').setValue('Subject')
    await wrapper.find('#contact-message').setValue('Message')

    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(wrapper.text()).toContain('contact.sendError')
  })
})
