import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'
import type { components } from '@/types/api'

export type TaskParticipant = components['schemas']['TaskParticipant']
export type DugnadHoursSummary = components['schemas']['DugnadHoursSummary']

export function useJoinTask() {
  const client = useApiClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (taskId: string) =>
      unwrap(await client.POST('/api/v1/tasks/{taskID}/join', {
        params: { path: { taskID: taskId } },
      })),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['projects'] })
    },
  })
}

export function useLeaveTask() {
  const client = useApiClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (taskId: string) =>
      unwrap(await client.DELETE('/api/v1/tasks/{taskID}/leave', {
        params: { path: { taskID: taskId } },
      })),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['projects'] })
    },
  })
}

export function useTaskParticipants(taskId: () => string) {
  const client = useApiClient()

  return useQuery({
    queryKey: ['task-participants', taskId],
    queryFn: async () =>
      unwrap(await client.GET('/api/v1/tasks/{taskID}/participants', {
        params: { path: { taskID: taskId() } },
      })),
    enabled: () => !!taskId(),
  })
}

export function useMyDugnadHours() {
  const client = useApiClient()

  return useQuery({
    queryKey: ['dugnad-hours', 'me'],
    queryFn: async () =>
      unwrap(await client.GET('/api/v1/members/me/dugnad-hours')),
  })
}

export function useAllDugnadHours() {
  const client = useApiClient()

  return useQuery({
    queryKey: ['dugnad-hours', 'all'],
    queryFn: async () =>
      unwrap(await client.GET('/api/v1/admin/dugnad/hours')),
  })
}

export function useSetRequiredHours() {
  const client = useApiClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (hours: number) =>
      unwrap(await client.PUT('/api/v1/admin/dugnad/settings/hours', {
        body: { hours } as any,
      })),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['dugnad-hours'] })
    },
  })
}
