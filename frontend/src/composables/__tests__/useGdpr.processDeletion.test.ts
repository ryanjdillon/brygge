import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useTotpGateStore } from '@/stores/totpGate'
import { useProcessDeletion } from '@/composables/useGdpr'

let mutationFn: (id: string) => Promise<unknown>

// Capture the mutation function so it runs against the real useFreshTotp
// replay logic, with only the network and the step-up modal faked.
vi.mock('@tanstack/vue-query', () => ({
  useQuery: vi.fn(),
  useMutation: (opts: { mutationFn: (id: string) => Promise<unknown> }) => {
    mutationFn = opts.mutationFn
    return {}
  },
  useQueryClient: () => ({ invalidateQueries: vi.fn() }),
}))

vi.mock('@/lib/apiClient', () => ({ useApiClient: () => ({}), unwrap: vi.fn() }))

const url = '/api/v1/admin/gdpr/deletion-requests/req-1/process'

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

describe('useProcessDeletion replay (BRY-509)', () => {
  const fetchMock = vi.fn()

  // The auth store reads the session on creation, so answer that call apart
  // and script only the process route's responses.
  function scriptProcessResponses(...responses: Response[]) {
    fetchMock.mockImplementation(async (input: string) =>
      input === url ? responses.shift()! : jsonResponse(401, {}),
    )
  }

  function processCalls() {
    return fetchMock.mock.calls.filter((c) => c[0] === url)
  }

  beforeEach(() => {
    setActivePinia(createPinia())
    fetchMock.mockReset()
    vi.stubGlobal('fetch', fetchMock)
    useProcessDeletion()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('re-prompts and replays once when the fresh window lapsed', async () => {
    scriptProcessResponses(
      jsonResponse(403, { error: 'totp_fresh_required' }),
      jsonResponse(200, { status: 'processed' }),
    )
    const open = vi.spyOn(useTotpGateStore(), 'open').mockResolvedValue(true)

    await expect(mutationFn('req-1')).resolves.toEqual({ status: 'processed' })

    expect(open).toHaveBeenCalledTimes(1)
    expect(processCalls()).toHaveLength(2)
    expect(processCalls()[1][1]).toMatchObject({ method: 'POST' })
  })

  it('does not replay when the re-prompt is cancelled, and surfaces the refusal', async () => {
    scriptProcessResponses(jsonResponse(403, { error: 'totp_fresh_required' }))
    vi.spyOn(useTotpGateStore(), 'open').mockResolvedValue(false)

    await expect(mutationFn('req-1')).rejects.toMatchObject({ status: 403, message: 'totp_fresh_required' })
    expect(processCalls()).toHaveLength(1)
  })
})
