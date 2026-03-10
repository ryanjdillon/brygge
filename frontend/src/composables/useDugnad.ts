import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'
import type { components } from '@/types/api'

export type TaskParticipant = components['schemas']['TaskParticipant']
export type DugnadHoursSummary = components['schemas']['DugnadHoursSummary']

export function useJoinTask() {
  const { fetchApi } = useApi()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (taskId: string) =>
      fetchApi(`/api/v1/tasks/${taskId}/join`, { method: 'POST' }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['projects'] })
    },
  })
}

export function useLeaveTask() {
  const { fetchApi } = useApi()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (taskId: string) =>
      fetchApi(`/api/v1/tasks/${taskId}/leave`, { method: 'DELETE' }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['projects'] })
    },
  })
}

export function useTaskParticipants(taskId: () => string) {
  const { fetchApi } = useApi()

  return useQuery({
    queryKey: ['task-participants', taskId],
    queryFn: () => fetchApi<TaskParticipant[]>(`/api/v1/tasks/${taskId()}/participants`),
    enabled: () => !!taskId(),
  })
}

export function useMyDugnadHours() {
  const { fetchApi } = useApi()

  return useQuery({
    queryKey: ['dugnad-hours', 'me'],
    queryFn: () => fetchApi<DugnadHoursSummary>('/api/v1/members/me/dugnad-hours'),
  })
}

export function useAllDugnadHours() {
  const { fetchApi } = useApi()

  return useQuery({
    queryKey: ['dugnad-hours', 'all'],
    queryFn: () => fetchApi<DugnadHoursSummary[]>('/api/v1/admin/dugnad/hours'),
  })
}

export function useSetRequiredHours() {
  const { fetchApi } = useApi()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (hours: number) =>
      fetchApi('/api/v1/admin/dugnad/settings/hours', {
        method: 'PUT',
        body: JSON.stringify({ hours }),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['dugnad-hours'] })
    },
  })
}
