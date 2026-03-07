<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'
import { Trash2 } from 'lucide-vue-next'

const { t } = useI18n()
const { fetchApi } = useApi()
const queryClient = useQueryClient()

interface User {
  id: string
  full_name: string
  email: string
  phone: string
  roles: string[]
  created_at: string
}

interface UsersResponse {
  users: User[]
  total_count: number
}

const { data: usersResponse, isLoading, isError } = useQuery({
  queryKey: ['admin', 'users'],
  queryFn: () => fetchApi<UsersResponse>('/api/v1/admin/users'),
})

const users = computed(() => usersResponse.value?.users ?? [])

const editingRoles = ref<Record<string, string[]>>({})

function startEditRoles(user: User) {
  editingRoles.value[user.id] = [...user.roles]
}

function cancelEditRoles(userId: string) {
  delete editingRoles.value[userId]
}

const { mutate: updateRoles } = useMutation({
  mutationFn: ({ userId, roles }: { userId: string; roles: string[] }) =>
    fetchApi(`/api/v1/admin/users/${userId}/roles`, {
      method: 'PUT',
      body: JSON.stringify({ roles }),
    }),
  onSuccess: (_, { userId }) => {
    delete editingRoles.value[userId]
    queryClient.invalidateQueries({ queryKey: ['admin', 'users'] })
  },
})

const { mutate: deleteUser } = useMutation({
  mutationFn: (userId: string) =>
    fetchApi(`/api/v1/admin/users/${userId}`, { method: 'DELETE' }),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['admin', 'users'] })
  },
})

const allRoles = ['member', 'slip_owner', 'styre', 'harbour_master', 'treasurer', 'admin']

function toggleRole(userId: string, role: string) {
  const roles = editingRoles.value[userId]
  if (!roles) return
  const idx = roles.indexOf(role)
  if (idx >= 0) roles.splice(idx, 1)
  else roles.push(role)
}

function confirmDelete(userId: string) {
  if (confirm('Er du sikker på at du vil slette denne brukeren?')) {
    deleteUser(userId)
  }
}
</script>

<template>
  <div>
    <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.sidebar.users') }}</h1>

    <div v-if="isLoading" class="mt-6 text-gray-500">{{ t('common.loading') }}...</div>
    <div v-else-if="isError" class="mt-6 rounded-md bg-red-50 p-3 text-sm text-red-800">Kunne ikke hente brukere</div>

    <div v-else class="mt-6 overflow-x-auto">
      <table class="min-w-full divide-y divide-gray-200">
        <thead class="bg-gray-50">
          <tr>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">Navn</th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">E-post</th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">Roller</th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('common.actions') }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 bg-white">
          <tr v-for="user in users" :key="user.id">
            <td class="whitespace-nowrap px-4 py-3 text-sm font-medium text-gray-900">{{ user.full_name }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ user.email }}</td>
            <td class="px-4 py-3 text-sm">
              <template v-if="editingRoles[user.id]">
                <div class="flex flex-wrap gap-1">
                  <button
                    v-for="role in allRoles"
                    :key="role"
                    :class="[
                      'rounded-full px-2 py-0.5 text-xs font-medium transition',
                      editingRoles[user.id].includes(role)
                        ? 'bg-blue-600 text-white'
                        : 'bg-gray-100 text-gray-600 hover:bg-gray-200',
                    ]"
                    @click="toggleRole(user.id, role)"
                  >
                    {{ role }}
                  </button>
                </div>
                <div class="mt-1 flex gap-1">
                  <button class="text-xs text-blue-600 hover:underline" @click="updateRoles({ userId: user.id, roles: editingRoles[user.id] })">Lagre</button>
                  <button class="text-xs text-gray-500 hover:underline" @click="cancelEditRoles(user.id)">Avbryt</button>
                </div>
              </template>
              <template v-else>
                <div class="flex flex-wrap gap-1">
                  <span
                    v-for="role in user.roles"
                    :key="role"
                    class="rounded-full bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-700 cursor-pointer hover:bg-gray-200"
                    @click="startEditRoles(user)"
                  >
                    {{ role }}
                  </span>
                </div>
              </template>
            </td>
            <td class="whitespace-nowrap px-4 py-3 text-sm">
              <button class="text-red-600 hover:text-red-800" @click="confirmDelete(user.id)">
                <Trash2 class="h-4 w-4" />
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
