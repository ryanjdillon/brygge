import { computed } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'

export interface Project {
  id: string
  club_id: string
  name: string
  description: string
  created_by: string
  created_at: string
  updated_at: string
  todo_count: number
  in_progress_count: number
  done_count: number
}

export interface Task {
  id: string
  project_id: string
  club_id: string
  title: string
  description: string
  assignee_id: string | null
  status: 'todo' | 'in_progress' | 'done'
  priority: 'low' | 'medium' | 'high'
  due_date: string | null
  created_by: string
  created_at: string
  updated_at: string
}

export interface GroupedTasks {
  todo: Task[]
  in_progress: Task[]
  done: Task[]
}

export interface CreateProjectInput {
  name: string
  description: string
}

export interface CreateTaskInput {
  title: string
  description: string
  assignee_id?: string
  due_date?: string
  priority: string
}

export interface UpdateTaskInput {
  title?: string
  description?: string
  assignee_id?: string
  status?: string
  priority?: string
  due_date?: string
}

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
