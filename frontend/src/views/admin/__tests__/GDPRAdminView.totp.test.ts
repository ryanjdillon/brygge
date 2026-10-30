import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import GDPRAdminView from '@/views/admin/GDPRAdminView.vue'

const calls: string[] = []
const ensureFreshTotp = vi.fn()
const totpAwareFetch = vi.fn()
const confirm = vi.fn()

let requestRows: typeof requests
const pastGrace = '2020-01-01T00:00:00Z'
const requests = [
  {
    id: 'req-1',
    user_name: 'Test Member',
    user_email: 'member@example.test',
    requested_at: '2019-12-01T00:00:00Z',
    grace_end: pastGrace,
  },
]

// The global setup stubs vue-query inertly; the process mutation's behaviour
// lives in its mutationFn, so run that for real and serve the two queries.
vi.mock('@tanstack/vue-query', () => ({
  useQuery: (opts: { queryKey: string[] }) => ({
    data: ref(opts.queryKey[1] === 'deletion-requests' ? { requests: requestRows } : { documents: [] }),
    isLoading: ref(false),
  }),
  useMutation: (opts: { mutationFn: (v: unknown) => Promise<unknown>; onSuccess?: () => void }) => ({
    mutate: (v: unknown, callbacks?: { onError?: (e: unknown) => void }) => {
      opts.mutationFn(v).then(() => opts.onSuccess?.(), (e) => callbacks?.onError?.(e))
    },
    isPending: ref(false),
  }),
  useQueryClient: () => ({ invalidateQueries: vi.fn() }),
}))

vi.mock('@/composables/useFreshTotp', () => ({
  useFreshTotp: () => ({
    ensureFreshTotp: () => ensureFreshTotp(),
    totpAwareFetch: (...args: unknown[]) => totpAwareFetch(...args),
  }),
}))

vi.mock('@/stores/confirm', () => ({ useConfirm: () => confirm }))

// The global i18n stub drops interpolation params; keep them so the confirm
// body's name is observable.
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({
    t: (key: string, params?: Record<string, string>) => (params ? `${key} ${JSON.stringify(params)}` : key),
    locale: ref('en'),
  }),
}))

const toastError = vi.fn()
vi.mock('@/composables/useToast', () => ({ useToast: () => ({ error: toastError }) }))

vi.mock('@/lib/apiClient', () => ({
  useApiClient: () => ({ GET: vi.fn() }),
  unwrap: (res: { data: unknown }) => res.data,
}))

async function clickProcess(rows = requests) {
  requestRows = rows
  const wrapper = mountWithPlugins(GDPRAdminView)
  await flushPromises()
  await wrapper.find('button.bg-red-600').trigger('click')
  await flushPromises()
  return wrapper
}

describe('GDPRAdminView process deletion step-up (BRY-509)', () => {
  beforeEach(() => {
    calls.length = 0
    ensureFreshTotp.mockReset().mockImplementation(async () => {
      calls.push('totp')
      return true
    })
    toastError.mockReset()
    confirm.mockReset().mockImplementation(async () => {
      calls.push('confirm')
      return true
    })
    totpAwareFetch.mockReset().mockImplementation(async () => {
      calls.push('fetch')
      return { ok: true, json: async () => ({ status: 'processed' }) }
    })
  })

  it('steps up on click, before the confirm dialog, then posts through totpAwareFetch', async () => {
    await clickProcess()
    expect(calls).toEqual(['totp', 'confirm', 'fetch'])
    expect(totpAwareFetch).toHaveBeenCalledTimes(1)
    expect(totpAwareFetch.mock.calls[0][0]).toBe('/api/v1/admin/gdpr/deletion-requests/req-1/process')
    expect(totpAwareFetch.mock.calls[0][1]).toMatchObject({ method: 'POST' })
  })

  it('does nothing when the step-up is cancelled: no dialog, no request', async () => {
    ensureFreshTotp.mockResolvedValue(false)
    await clickProcess()
    expect(confirm).not.toHaveBeenCalled()
    expect(totpAwareFetch).not.toHaveBeenCalled()
  })

  it('sends no request when the confirm dialog is declined', async () => {
    confirm.mockResolvedValue(false)
    await clickProcess()
    expect(ensureFreshTotp).toHaveBeenCalledTimes(1)
    expect(totpAwareFetch).not.toHaveBeenCalled()
  })

  it('falls back to generic wording when the request has no user name', async () => {
    await clickProcess([{ ...requests[0], user_name: '' }])
    expect(confirm).toHaveBeenCalledTimes(1)
    expect(confirm.mock.calls[0][0].body).toContain('"name":"gdpr.admin.thisMember"')
  })

  it('names the member in the confirm dialog when the request has a name', async () => {
    await clickProcess()
    expect(confirm.mock.calls[0][0].body).toContain('"name":"Test Member"')
  })

  it('shows an error toast when the request fails', async () => {
    totpAwareFetch.mockResolvedValue({
      ok: false,
      status: 500,
      statusText: 'Internal Server Error',
      json: async () => ({ error: 'internal error' }),
    })
    await clickProcess()
    expect(toastError).toHaveBeenCalledTimes(1)
  })

  it('shows nothing alarming when the re-prompt is cancelled', async () => {
    totpAwareFetch.mockResolvedValue({
      ok: false,
      status: 403,
      statusText: 'Forbidden',
      json: async () => ({ error: 'totp_fresh_required' }),
    })
    await clickProcess()
    expect(toastError).not.toHaveBeenCalled()
  })
})
