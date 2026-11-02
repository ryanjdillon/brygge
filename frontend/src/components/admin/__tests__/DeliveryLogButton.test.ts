import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import DeliveryLogButton from '@/components/admin/DeliveryLogButton.vue'

const totpAwareFetch = vi.fn()

vi.mock('@/composables/useFreshTotp', () => ({
  useFreshTotp: () => ({ totpAwareFetch: (...args: unknown[]) => totpAwareFetch(...args) }),
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
  beforeEach(() => totpAwareFetch.mockReset())
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
})
