import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'

export interface TaskParticipant {
  task_id: string
  user_id: string
  role: 'ansvarlig' | 'collaborator'
  hours: number | null
  joined_at: string
  name: string
}

export interface DugnadHoursSummary {
  user_id: string
  name: string
  signed_up_hours: number
  completed_hours: number
  required_hours: number
  remaining: number
}

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
