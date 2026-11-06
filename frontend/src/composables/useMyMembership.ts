import { computed } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'
import { useAuthStore } from '@/stores/auth'
import type { components } from '@/types/api'

export type MyMembership = components['schemas']['MyMembership']

// Logout does not clear the query cache, so the key carries the user: a
// different member logging in on the same tab never sees the last one's state.
export const myMembershipKeyPrefix = ['my-membership'] as const

function useMyMembershipKey() {
  const auth = useAuthStore()
  return computed(() => [...myMembershipKeyPrefix, auth.user?.id ?? ''])
}

/** The member's membership state and what cancelling it affects (BRY-633). */
export function useMyMembership() {
  const client = useApiClient()
  return useQuery({
    queryKey: useMyMembershipKey(),
    queryFn: async () => unwrap(await client.GET('/api/v1/members/me/membership')),
  })
}

/** Cancels the membership at year end; idempotent on the server. */
export function useCancelMembership() {
  const client = useApiClient()
  const queryClient = useQueryClient()
  const key = useMyMembershipKey()
  return useMutation({
    mutationFn: async (reason: string) =>
      unwrap(
        await client.POST('/api/v1/members/me/membership/cancel', {
          body: reason ? { reason } : {},
        }),
      ),
    onSuccess: (data) => queryClient.setQueryData(key.value, data),
  })
}

/**
 * Undoes a cancellation while the member is still active. A refusal means
 * the state moved on (the membership ended), so the cached state is refetched.
 */
export function useUndoCancelMembership() {
  const client = useApiClient()
  const queryClient = useQueryClient()
  const key = useMyMembershipKey()
  return useMutation({
    mutationFn: async () => unwrap(await client.POST('/api/v1/members/me/membership/undo')),
    onSuccess: (data) => queryClient.setQueryData(key.value, data),
    onError: () => queryClient.invalidateQueries({ queryKey: [...myMembershipKeyPrefix] }),
  })
}
