import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import DeliveryLogButton from '@/components/admin/DeliveryLogButton.vue'

const totpAwareFetch = vi.fn()
const ensureFreshTotp = vi.fn()
const askConfirm = vi.fn()
const toastSuccess = vi.fn()
const toastError = vi.fn()

vi.mock('@/composables/useFreshTotp', () => ({
  useFreshTotp: () => ({
    ensureFreshTotp: () => ensureFreshTotp(),
    totpAwareFetch: (...args: unknown[]) => totpAwareFetch(...args),
  }),
}))

vi.mock('@/stores/confirm', () => ({
  useConfirm: () => (opts: unknown) => askConfirm(opts),
}))

vi.mock('@/composables/useToast', () => ({
  useToast: () => ({ success: toastSuccess, error: toastError, info: vi.fn() }),
}))

function respond(body: unknown) {
  totpAwareFetch.mockResolvedValue({ ok: true, json: async () => body })
}

async function openLog() {
  const wrapper = mountWithPlugins(DeliveryLogButton, {
    props: { invoiceId: 'inv-1' },
    attachTo: document.body,
  })
  await wrapper.find('button').trigger('click')
  await flushPromises()
  return wrapper
}

describe('DeliveryLogButton', () => {
  beforeEach(() => {
    totpAwareFetch.mockReset()
    ensureFreshTotp.mockReset()
    askConfirm.mockReset()
    toastSuccess.mockReset()
    toastError.mockReset()
  })
  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('shows each send with its outcome, and explains a send with no record', async () => {
    respond({
      available: true,
      recipient_email: 'medlem@example.org',
      sends: [
        { kind: 'reminder', at: '2026-11-02T10:00:00Z', status: 'no_record', events: [] },
        {
          kind: 'sent',
          at: '2026-11-01T18:00:00Z',
          status: 'accepted',
          smtp_code: 250,
          events: [
            { timestamp: '2026-11-01T18:00:01Z', message: 'SMTP RCPT TO rejected', smtp_code: 450, hostname: 'mx.example.org' },
            { timestamp: '2026-11-01T18:05:01Z', message: 'Message delivered', smtp_code: 250, hostname: 'mx.example.org' },
          ],
        },
      ],
    })
    const wrapper = await openLog()

    expect(totpAwareFetch).toHaveBeenCalledWith('/api/v1/admin/financials/invoices/inv-1/delivery-log')
    const statuses = [...document.body.querySelectorAll('[data-testid="delivery-status"]')].map((n) => n.textContent?.trim())
    expect(statuses).toEqual(['admin.faktura.sent.deliveryLog.status.no_record', 'admin.faktura.sent.deliveryLog.status.accepted'])
    expect(document.body.textContent).toContain('admin.faktura.sent.deliveryLog.statusHelp.no_record')
    expect(document.body.textContent).toContain('Message delivered')
    expect(document.body.textContent).toContain('450')
    wrapper.unmount()
  })

  it('says the faktura was never emailed when there are no sends', async () => {
    respond({ available: true, recipient_email: 'medlem@example.org', sends: [] })
    const wrapper = await openLog()
    expect(document.body.textContent).toContain('admin.faktura.sent.deliveryLog.noSends')
    wrapper.unmount()
  })

  it('shows the next retry for a delayed send', async () => {
    respond({
      available: true,
      recipient_email: 'medlem@example.org',
      sends: [
        { kind: 'sent', at: '2026-11-01T18:00:00Z', status: 'deferred', smtp_code: 451, next_retry_at: '2026-11-01T18:10:00Z', events: [] },
      ],
    })
    const wrapper = await openLog()
    expect(document.body.textContent).toContain('admin.faktura.sent.deliveryLog.status.deferred')
    expect(document.body.textContent).toContain('admin.faktura.sent.deliveryLog.nextRetry')
    wrapper.unmount()
  })

  const suppressedLog = {
    available: true,
    recipient_email: 'medlem@example.org',
    sends: [
      {
        id: 'send-2',
        kind: 'reminder',
        at: '2026-11-02T10:00:00Z',
        status: 'suppressed',
        provider: 'ses',
        recipient: 'medlem@example.org',
        can_unsuppress: true,
        events: [
          {
            timestamp: '2026-11-02T10:00:01Z',
            type: 'bounce',
            subtype: 'Permanent/OnAccountSuppressionList',
            message: 'Bounced',
            details: 'Amazon SES did not send the message',
          },
        ],
      },
      {
        id: 'send-1',
        kind: 'sent',
        at: '2026-11-01T18:00:00Z',
        status: 'complaint',
        provider: 'ses',
        recipient: 'gammel@example.org',
        events: [],
      },
    ],
  }

  it('explains a suppressed send and offers removal only where the server allows it', async () => {
    respond(suppressedLog)
    const wrapper = await openLog()
    const statuses = [...document.body.querySelectorAll('[data-testid="delivery-status"]')].map((n) => n.textContent?.trim())
    expect(statuses).toEqual(['admin.faktura.sent.deliveryLog.status.suppressed', 'admin.faktura.sent.deliveryLog.status.complaint'])
    expect(document.body.textContent).toContain('admin.faktura.sent.deliveryLog.statusHelp.suppressed')
    expect(document.body.textContent).toContain('admin.faktura.sent.deliveryLog.statusHelp.complaint')
    // SES events are labelled by type, with the bounce subtype shown.
    expect(document.body.textContent).toContain('admin.faktura.sent.deliveryLog.eventType.bounce')
    expect(document.body.textContent).toContain('Permanent/OnAccountSuppressionList')
    // A send to an older address names it.
    expect(document.body.querySelectorAll('[data-testid="delivery-send-recipient"]')).toHaveLength(1)
    // One button: the complaint send never offers it.
    expect(document.body.querySelectorAll('[data-testid="delivery-unsuppress"]')).toHaveLength(1)
    wrapper.unmount()
  })

  it('asks for the step-up, then confirms, then removes the address', async () => {
    respond(suppressedLog)
    const wrapper = await openLog()
    const order: string[] = []
    ensureFreshTotp.mockImplementation(async () => {
      order.push('totp')
      return true
    })
    askConfirm.mockImplementation(async () => {
      order.push('confirm')
      return true
    })
    totpAwareFetch.mockImplementation(async () => {
      order.push('post')
      return { ok: true, json: async () => ({ removed: true, recipient_email: 'medlem@example.org' }) }
    })
    ;(document.body.querySelector('[data-testid="delivery-unsuppress"]') as HTMLButtonElement).click()
    await flushPromises()

    expect(order).toEqual(['totp', 'confirm', 'post'])
    expect(totpAwareFetch).toHaveBeenLastCalledWith(
      '/api/v1/admin/financials/invoices/inv-1/email-sends/send-2/unsuppress',
      { method: 'POST' },
    )
    expect(toastSuccess).toHaveBeenCalledWith('admin.faktura.sent.deliveryLog.unsuppress.done')
    expect(document.body.querySelectorAll('[data-testid="delivery-unsuppress"]')).toHaveLength(0)
    wrapper.unmount()
  })

  it('does nothing when the step-up or the confirm is cancelled', async () => {
    respond(suppressedLog)
    const wrapper = await openLog()
    totpAwareFetch.mockClear()
    ensureFreshTotp.mockResolvedValue(false)
    ;(document.body.querySelector('[data-testid="delivery-unsuppress"]') as HTMLButtonElement).click()
    await flushPromises()
    expect(askConfirm).not.toHaveBeenCalled()

    ensureFreshTotp.mockResolvedValue(true)
    askConfirm.mockResolvedValue(false)
    ;(document.body.querySelector('[data-testid="delivery-unsuppress"]') as HTMLButtonElement).click()
    await flushPromises()
    expect(totpAwareFetch).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('reports a failed removal and keeps the button', async () => {
    respond(suppressedLog)
    const wrapper = await openLog()
    ensureFreshTotp.mockResolvedValue(true)
    askConfirm.mockResolvedValue(true)
    totpAwareFetch.mockResolvedValue({ ok: false, status: 503, statusText: 'Service Unavailable', json: async () => ({ error: 'Amazon SES is not configured on this server' }) })
    ;(document.body.querySelector('[data-testid="delivery-unsuppress"]') as HTMLButtonElement).click()
    await flushPromises()
    expect(toastError).toHaveBeenCalledWith('admin.faktura.sent.deliveryLog.unsuppress.failed')
    expect(document.body.querySelectorAll('[data-testid="delivery-unsuppress"]')).toHaveLength(1)
    wrapper.unmount()
  })
})
