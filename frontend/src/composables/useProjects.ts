import { computed } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'
import type { components } from '@/types/api'

export type Project = components['schemas']['ProjectWithCounts']
export type MaterialItem = components['schemas']['MaterialItem']
export type Task = components['schemas']['Task']
export type GroupedTasks = components['schemas']['GroupedTasks']

export interface CreateProjectInput {
  name: string
  description: string
}

export type CreateTaskInput = Partial<Task> & { title: string; priority: string }
export type UpdateTaskInput = Partial<Task>

export function useProjects() {
  const { fetchApi } = useApi()

  return useQuery({
    queryKey: ['projects'],
    queryFn: () => fetchApi<Project[]>('/api/v1/projects'),
  })
}

export function useProject(projectId: () => string) {
  const { fetchApi } = useApi()
  const id = computed(() => projectId())

  return useQuery({
    queryKey: ['projects', id],
    queryFn: () => fetchApi<Project>(`/api/v1/projects/${id.value}`),
    enabled: () => !!id.value,
  })
}

export function useProjectTasks(projectId: () => string) {
  const { fetchApi } = useApi()
  const id = computed(() => projectId())

  return useQuery({
    queryKey: ['projects', id, 'tasks'],
    queryFn: () => fetchApi<GroupedTasks>(`/api/v1/projects/${id.value}/tasks`),
    enabled: () => !!id.value,
  })
}

export function useCreateProject() {
  const { fetchApi } = useApi()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (input: CreateProjectInput) =>
      fetchApi<Project>('/api/v1/projects', {
        method: 'POST',
        body: JSON.stringify(input),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['projects'] })
    },
  })
}

export function useCreateTask(projectId: () => string) {
  const { fetchApi } = useApi()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (input: CreateTaskInput) =>
      fetchApi<Task>(`/api/v1/projects/${projectId()}/tasks`, {
        method: 'POST',
        body: JSON.stringify(input),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['projects'] })
    },
  })
}

export function useUpdateTask() {
  const { fetchApi } = useApi()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ taskId, input }: { taskId: string; input: UpdateTaskInput }) =>
      fetchApi<Task>(`/api/v1/tasks/${taskId}`, {
        method: 'PUT',
        body: JSON.stringify(input),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['projects'] })
    },
  })
}

export function useDeleteTask() {
  const { fetchApi } = useApi()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (taskId: string) =>
      fetchApi(`/api/v1/tasks/${taskId}`, { method: 'DELETE' }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['projects'] })
    },
  })
}
