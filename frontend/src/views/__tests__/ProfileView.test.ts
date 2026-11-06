import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref, nextTick } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { mountWithPlugins } from '@/test/test-utils'
import { ApiError } from '@/lib/errors'
import ProfileView from '@/views/portal/ProfileView.vue'

const recordConsent = vi.fn()
const put = vi.fn()

vi.mock('@/lib/apiClient', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/apiClient')>()),
  useApiClient: () => ({ GET: vi.fn(), PUT: put }),
}))

vi.mock('@/composables/useGdpr', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/composables/useGdpr')>()),
  useLegalDocument: () => ({ data: ref({ version: '2026-1' }) }),
  useMyConsents: () => ({ consents: ref([]) }),
  useRecordConsent: () => ({ mutateAsync: recordConsent }),
}))

interface MutationOptions {
  mutationFn: () => Promise<unknown>
  onError?: (err: unknown) => void
}

// The profile save is the mutation with an onError: the others on the page
// (email preferences) are captured too and skipped.
function mountAndCaptureSave() {
  const captured: MutationOptions[] = []
  vi.mocked(useMutation).mockImplementation(((opts: MutationOptions) => {
    captured.push(opts)
    return { mutate: vi.fn(), mutateAsync: vi.fn(), isPending: ref(false) }
  }) as unknown as typeof useMutation)
  const invalidateQueries = vi.fn()
  vi.mocked(useQueryClient).mockReturnValue({ invalidateQueries } as unknown as ReturnType<typeof useQueryClient>)

  const wrapper = mountWithPlugins(ProfileView)
  const save = captured.find((o) => o.onError)
  if (!save) throw new Error('profile save mutation not found')
  return { wrapper, save, invalidateQueries }
}

describe('ProfileView privacy consent', () => {
  beforeEach(() => {
    recordConsent.mockReset()
    put.mockReset()
  })

  it('sends the version the member was shown as the expected version', async () => {
    recordConsent.mockResolvedValue({})
    put.mockResolvedValue({ data: {}, response: new Response(null, { status: 200 }) })
    const { save } = mountAndCaptureSave()

    await save.mutationFn()

    expect(recordConsent).toHaveBeenCalledWith({ consent_type: 'privacy', version: '2026-1' })
  })

  it.each(['legal_document_stale', 'legal_document_unpublished'])(
    'on 409 %s reloads the policy and asks again instead of the generic error',
    async (code) => {
      const { wrapper, save, invalidateQueries } = mountAndCaptureSave()

      save.onError?.(new ApiError(409, 'conflict', code))
      await nextTick()

      expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ['legal-document', 'privacy'] })
      expect(wrapper.text()).toContain('portal.profile.privacyChanged')
      expect(wrapper.text()).not.toContain('portal.profile.saveError')
    },
  )

  it('keeps the generic error for other failures', async () => {
    const { wrapper, save, invalidateQueries } = mountAndCaptureSave()

    save.onError?.(new ApiError(500, 'internal error'))
    await nextTick()

    expect(invalidateQueries).not.toHaveBeenCalledWith({ queryKey: ['legal-document', 'privacy'] })
    expect(wrapper.text()).toContain('portal.profile.saveError')
  })
})
