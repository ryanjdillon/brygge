import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import { useAuthStore } from '@/stores/auth'
import ApplicantCompleteView from '@/views/ApplicantCompleteView.vue'

const client = { GET: vi.fn(), POST: vi.fn() }

vi.mock('@/lib/apiClient', async () => {
  const actual = await vi.importActual<typeof import('@/lib/apiClient')>('@/lib/apiClient')
  return { useApiClient: () => client, unwrap: actual.unwrap, ApiError: actual.ApiError }
})

function ok(data: unknown, status = 200) {
  return { data, response: new Response(null, { status }) }
}
function fail(status: number, code: string) {
  return { error: { error: 'refused', code }, response: new Response(null, { status }) }
}

const PAY_URL = `${window.location.origin}/betal/order-1`
const INVOICE = {
  invoice_id: 'inv-1',
  invoice_number: 1001,
  kid_number: '0000100107',
  account_number: '12345678903',
  amount: 650,
  currency: 'NOK',
  due_date: '2026-11-21',
  emailed: true,
}

async function mountPage() {
  vi.spyOn(globalThis, 'fetch').mockImplementation(() => new Promise(() => {}))
  const wrapper = mountWithPlugins(ApplicantCompleteView, {
    piniaOptions: { initialState: { auth: { user: { roles: ['applicant'] } } } },
  })
  await flushPromises()
  return wrapper
}

describe('ApplicantCompleteView', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    client.GET.mockReset()
    client.POST.mockReset()
  })

  it('loads the join state without issuing anything', async () => {
    client.GET.mockResolvedValue(ok({ state: 'awaiting_payment', checkout_url: PAY_URL }))
    const wrapper = await mountPage()
    expect(client.GET).toHaveBeenCalledWith('/api/v1/join/me')
    expect(client.POST).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="complete-awaiting"]').exists()).toBe(true)
  })

  it('opens the pay page in the app from "Betal no"', async () => {
    client.GET.mockResolvedValue(ok({ state: 'awaiting_payment', checkout_url: PAY_URL }))
    const wrapper = await mountPage()
    const push = vi.spyOn(wrapper.vm.$router, 'push').mockResolvedValue(undefined)
    await wrapper.find('[data-testid="complete-pay-now"]').trigger('click')
    await flushPromises()
    expect(push).toHaveBeenCalledWith('/betal/order-1')
  })

  it('issues the bank faktura on request and shows the KID, account, amount and due date to copy', async () => {
    client.GET.mockResolvedValue(ok({ state: 'awaiting_payment', checkout_url: PAY_URL }))
    client.POST.mockResolvedValue(ok(INVOICE, 201))
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const wrapper = await mountPage()

    await wrapper.find('[data-testid="complete-pay-bank"]').trigger('click')
    await flushPromises()

    expect(client.POST).toHaveBeenCalledWith('/api/v1/join/bank-invoice')
    expect(wrapper.find('[data-testid="complete-bank-invoice"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('join.complete.waitingForPayment')
    expect(wrapper.find('[data-testid="complete-kid"]').text()).toBe('0000100107')
    expect(wrapper.find('[data-testid="complete-account"]').text()).toBe('1234.56.78903')
    expect(wrapper.find('[data-testid="complete-amount"]').text()).toContain('650')
    expect(wrapper.find('[data-testid="complete-due"]').text()).toMatch(/21.*2026|2026.*21/)
    expect(wrapper.find('[data-testid="complete-emailed"]').exists()).toBe(true)

    await wrapper.find('[data-testid="complete-copy-kid"]').trigger('click')
    await wrapper.find('[data-testid="complete-copy-account"]').trigger('click')
    await wrapper.find('[data-testid="complete-copy-amount"]').trigger('click')
    expect(writeText.mock.calls.map((c) => c[0])).toEqual(['0000100107', '12345678903', '650,00'])
  })

  it('asks to finish or cancel a card or Vipps payment on payment_in_progress', async () => {
    client.GET.mockResolvedValue(ok({ state: 'awaiting_payment', checkout_url: PAY_URL }))
    client.POST.mockResolvedValue(fail(409, 'payment_in_progress'))
    const wrapper = await mountPage()
    await wrapper.find('[data-testid="complete-pay-bank"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="complete-in-progress"]').text()).toBe('join.complete.paymentInProgress')
    expect(wrapper.find('[data-testid="complete-awaiting"]').exists()).toBe(true)
  })

  it('reloads the state when the join was paid or ended meanwhile', async () => {
    client.GET.mockResolvedValueOnce(ok({ state: 'awaiting_payment', checkout_url: PAY_URL }))
    client.GET.mockResolvedValueOnce(ok({ state: 'member' }))
    client.POST.mockResolvedValue(fail(409, 'no_pending_join'))
    const wrapper = await mountPage()
    await wrapper.find('[data-testid="complete-pay-bank"]').trigger('click')
    await flushPromises()
    expect(client.GET).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-testid="complete-member"]').exists()).toBe(true)
  })

  it('shows an issued bank faktura as waiting for payment, with the online option', async () => {
    client.GET.mockResolvedValue(ok({ state: 'bank_invoice', checkout_url: PAY_URL, bank_invoice: { ...INVOICE, emailed: false } }))
    const wrapper = await mountPage()
    expect(wrapper.find('[data-testid="complete-bank-invoice"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="complete-kid"]').text()).toBe('0000100107')
    expect(wrapper.find('[data-testid="complete-emailed"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="complete-pay-online-instead"]').exists()).toBe(true)
    expect(client.POST).not.toHaveBeenCalled()
  })

  it('welcomes a new member and reloads the session', async () => {
    client.GET.mockResolvedValue(ok({ state: 'member' }))
    const wrapper = await mountPage()
    expect(useAuthStore().checkSession).toHaveBeenCalled()
    expect(wrapper.find('[data-testid="complete-member"]').text()).toContain('join.complete.welcomeTitle')
    expect(wrapper.find('[data-testid="complete-portal"]').attributes('href')).toBe('/portal')
  })

  it('points to the board when there is nothing left to pay', async () => {
    client.GET.mockResolvedValue(ok({ state: 'none' }))
    const wrapper = await mountPage()
    expect(wrapper.find('[data-testid="complete-none"]').find('a').attributes('href')).toBe('/contact')
  })
})
