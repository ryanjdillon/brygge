import { computed } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'
import type { components } from '@/types/api'

export type Project = components['schemas']['ProjectWithCounts']
export type MaterialItem = components['schemas']['MaterialItem']
export type Task = components['schemas']['Task']
export interface CreateProjectInput {
  name: string
  description: string
}

export type CreateTaskInput = Partial<Task> & { title: string; priority: string }
export type UpdateTaskInput = Partial<Task>

export function useProjects() {
  const client = useApiClient()

  return useQuery({
    queryKey: ['projects'],
    queryFn: async () =>
      unwrap(await client.GET('/api/v1/projects')),
  })
}

export function useProject(projectId: () => string) {
  const client = useApiClient()
  const id = computed(() => projectId())

  return useQuery({
    queryKey: ['projects', id],
    queryFn: async () =>
      unwrap(await client.GET('/api/v1/projects/{projectID}', {
        params: { path: { projectID: id.value } },
      })),
    enabled: () => !!id.value,
  })
}

export function useProjectTasks(projectId: () => string) {
  const client = useApiClient()
  const id = computed(() => projectId())

  return useQuery({
    queryKey: ['projects', id, 'tasks'],
    queryFn: async () =>
      unwrap(await client.GET('/api/v1/projects/{projectID}/tasks', {
        params: { path: { projectID: id.value } },
      })),
    enabled: () => !!id.value,
  })
}

export function useCreateProject() {
  const client = useApiClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (input: CreateProjectInput) =>
      unwrap(await client.POST('/api/v1/projects', { body: input as any })),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['projects'] })
    },
  })
}

export function useCreateTask(projectId: () => string) {
  const client = useApiClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (input: CreateTaskInput) =>
      unwrap(await client.POST('/api/v1/projects/{projectID}/tasks', {
        params: { path: { projectID: projectId() } },
        body: input as any,
      })),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['projects'] })
    },
  })
}

export function useUpdateTask() {
  const client = useApiClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async ({ taskId, input }: { taskId: string; input: UpdateTaskInput }) =>
      unwrap(await client.PUT('/api/v1/tasks/{taskID}', {
        params: { path: { taskID: taskId } },
        body: input as any,
      })),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['projects'] })
    },
  })
}

export function useDeleteTask() {
  const client = useApiClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (taskId: string) =>
      unwrap(await client.DELETE('/api/v1/tasks/{taskID}', {
        params: { path: { taskID: taskId } },
      })),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['projects'] })
    },
  })
}
