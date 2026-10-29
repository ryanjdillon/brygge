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
  Map: { template: '<span data-icon="map" />' },
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
  })

  it('hides the Matrix room row while the forum is disabled (BRY-191)', () => {
    const wrapper = mountWithPlugins(ContactView)
    expect(wrapper.text()).not.toContain('contact.matrixRoom')
  })

  it('limits field lengths to what the server accepts', () => {
    const wrapper = mountWithPlugins(ContactView)

    expect(wrapper.find('#contact-name').attributes('maxlength')).toBe('100')
    expect(wrapper.find('#contact-email').attributes('maxlength')).toBe('254')
    expect(wrapper.find('#contact-subject').attributes('maxlength')).toBe('200')
    expect(wrapper.find('#contact-message').attributes('maxlength')).toBe('5000')
    expect(wrapper.find('#contact-message').attributes('minlength')).toBe('10')
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
        website: '',
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

  it('renders a hidden honeypot field that people never reach', () => {
    const wrapper = mountWithPlugins(ContactView)
    const input = wrapper.find('input[name="website"]')

    expect(input.exists()).toBe(true)
    expect(input.attributes('tabindex')).toBe('-1')
    expect(input.attributes('autocomplete')).toBe('off')
    expect(input.element.closest('[aria-hidden="true"]')).not.toBeNull()
  })

  it('sends the honeypot value so the server can drop the message', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response(JSON.stringify({ status: 'received' }), { status: 200 }),
    )
    const wrapper = mountWithPlugins(ContactView)

    await wrapper.find('#contact-website').setValue('http://spam.example')
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    const init = fetchSpy.mock.calls[0][1] as RequestInit
    expect(JSON.parse(init.body as string).website).toBe('http://spam.example')
  })

  it.each([
    [429, 'contact.rateLimited'],
    [502, 'contact.deliveryFailed'],
    [503, 'contact.unavailable'],
    [400, 'contact.checkInput'],
    [413, 'contact.checkInput'],
    [500, 'contact.sendError'],
  ])('shows the matching message for a %i answer and never claims success', async (status, key) => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response(JSON.stringify({ error: 'x' }), { status }),
    )
    const wrapper = mountWithPlugins(ContactView)

    await wrapper.find('#contact-name').setValue('John')
    await wrapper.find('#contact-email').setValue('john@example.com')
    await wrapper.find('#contact-subject').setValue('Subject')
    await wrapper.find('#contact-message').setValue('A long enough message')
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(wrapper.text()).toContain(key)
    expect(wrapper.text()).not.toContain('contact.sent')
    // The visitor's text is kept so they can retry without retyping.
    expect((wrapper.find('#contact-message').element as HTMLTextAreaElement).value).toBe('A long enough message')
  })
})
