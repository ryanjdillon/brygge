<script setup lang="ts">
import { ref, computed, reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'
import { Trash2, UserPlus, Upload, X, ArrowUp, ArrowDown, Pencil } from 'lucide-vue-next'
import type { components } from '@/types/api'
import { formatName } from '@/lib/format'
import { useAuthStore } from '@/stores/auth'
import { useTotpGateStore } from '@/stores/totpGate'

type User = components['schemas']['AdminUser']
type CreateBody = components['schemas']['AdminUserCreate']
type UpdateBody = components['schemas']['AdminUserUpdate']
type ImportRow = components['schemas']['ImportUsersResultRow']

const { t } = useI18n()
const client = useApiClient()
const queryClient = useQueryClient()
const auth = useAuthStore()
const totpGate = useTotpGateStore()

async function ensureFreshTotp(): Promise<boolean> {
  if (auth.hasFreshTotp) return true
  return totpGate.open()
}

type SortField = 'first_name' | 'last_name' | 'email'
const sortField = ref<SortField>('last_name')
const sortDir = ref<'asc' | 'desc'>('asc')
const sortParam = computed(() => (sortDir.value === 'desc' ? '-' : '') + sortField.value)

const PAGE_SIZE = 100
const offset = ref(0)

const { data: usersResponse, isLoading, isError } = useQuery({
  queryKey: ['admin', 'users', sortParam, offset],
  queryFn: async () =>
    unwrap(
      await client.GET('/api/v1/admin/users', {
        params: { query: { limit: PAGE_SIZE, offset: offset.value, sort: sortParam.value } as any },
      }),
    ),
})

const users = computed(() => usersResponse.value?.users ?? [])
const totalCount = computed(() => usersResponse.value?.total_count ?? 0)
const pageStart = computed(() => (totalCount.value === 0 ? 0 : offset.value + 1))
const pageEnd = computed(() => Math.min(offset.value + users.value.length, totalCount.value))
const hasPrev = computed(() => offset.value > 0)
const hasNext = computed(() => offset.value + PAGE_SIZE < totalCount.value)

function setSort(field: SortField) {
  if (sortField.value === field) {
    sortDir.value = sortDir.value === 'asc' ? 'desc' : 'asc'
  } else {
    sortField.value = field
    sortDir.value = 'asc'
  }
  offset.value = 0
}

function nextPage() { if (hasNext.value) offset.value += PAGE_SIZE }
function prevPage() { if (hasPrev.value) offset.value = Math.max(0, offset.value - PAGE_SIZE) }

const editingRoles = ref<Record<string, string[]>>({})

async function startEditRoles(user: User) {
  if (!(await ensureFreshTotp())) return
  editingRoles.value[user.id] = [...(user.roles ?? [])]
}

function cancelEditRoles(userId: string) {
  delete editingRoles.value[userId]
}

const { mutate: updateRoles } = useMutation({
  mutationFn: async ({ userId, roles }: { userId: string; roles: string[] }) =>
    unwrap(await client.PUT('/api/v1/admin/users/{userID}/roles', {
      params: { path: { userID: userId } },
      body: { roles } as any,
    })),
  onSuccess: (_, { userId }) => {
    delete editingRoles.value[userId]
    queryClient.invalidateQueries({ queryKey: ['admin', 'users'] })
  },
})

const { mutate: deleteUser } = useMutation({
  mutationFn: async (userId: string) =>
    unwrap(await client.DELETE('/api/v1/admin/users/{userID}', { params: { path: { userID: userId } } })),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['admin', 'users'] })
  },
})

const allRoles = ['member', 'slip_holder', 'board', 'harbor_master', 'treasurer', 'admin']

function toggleRole(userId: string, role: string) {
  const roles = editingRoles.value[userId]
  if (!roles) return
  const idx = roles.indexOf(role)
  if (idx >= 0) roles.splice(idx, 1)
  else roles.push(role)
}

async function confirmDelete(userId: string) {
  if (!(await ensureFreshTotp())) return
  if (confirm(t('admin.users.deleteConfirm'))) {
    deleteUser(userId)
  }
}

// --- Detail + edit modal ---
const detailUser = ref<User | null>(null)
const detailEditing = ref(false)
const detailError = ref<string | null>(null)
const editForm = reactive<UpdateBody>({})

function openDetail(user: User) {
  detailUser.value = user
  detailEditing.value = false
  detailError.value = null
}

function closeDetail() {
  detailUser.value = null
  detailEditing.value = false
}

async function startDetailEdit() {
  if (!detailUser.value) return
  if (!(await ensureFreshTotp())) return
  const u = detailUser.value
  Object.assign(editForm, {
    first_name: u.first_name ?? '',
    last_name: u.last_name ?? '',
    phone: u.phone ?? '',
    address_line: u.address_line ?? '',
    postal_code: u.postal_code ?? '',
    city: u.city ?? '',
    is_local: !!u.is_local,
  })
  detailError.value = null
  detailEditing.value = true
}

async function openEditDirect(user: User) {
  openDetail(user)
  await startDetailEdit()
}

const { mutate: updateUser, isPending: savingEdit } = useMutation({
  mutationFn: async ({ userId, body }: { userId: string; body: UpdateBody }) =>
    unwrap(await client.PATCH('/api/v1/admin/users/{userID}', {
      params: { path: { userID: userId } },
      body,
    })),
  onSuccess: () => {
    detailEditing.value = false
    closeDetail()
    queryClient.invalidateQueries({ queryKey: ['admin', 'users'] })
  },
  onError: (err: any) => {
    detailError.value = err?.message ?? t('admin.users.updateError')
  },
})

function submitEdit() {
  if (!detailUser.value) return
  detailError.value = null
  updateUser({ userId: detailUser.value.id, body: { ...editForm } })
}

// --- Create user modal ---
const showCreateModal = ref(false)
const createError = ref<string | null>(null)
const blankForm = (): CreateBody & { roles: string[] } => ({
  email: '',
  first_name: '',
  last_name: '',
  phone: '',
  address_line: '',
  postal_code: '',
  city: '',
  is_local: false,
  roles: [],
})
const createForm = reactive<CreateBody & { roles: string[] }>(blankForm())

async function openCreateModal() {
  if (!(await ensureFreshTotp())) return
  Object.assign(createForm, blankForm())
  createError.value = null
  showCreateModal.value = true
}

function toggleCreateRole(role: string) {
  const idx = createForm.roles.indexOf(role)
  if (idx >= 0) createForm.roles.splice(idx, 1)
  else createForm.roles.push(role)
}

const { mutate: createUser, isPending: creating } = useMutation({
  mutationFn: async (body: CreateBody) =>
    unwrap(await client.POST('/api/v1/admin/users', { body })),
  onSuccess: () => {
    showCreateModal.value = false
    queryClient.invalidateQueries({ queryKey: ['admin', 'users'] })
  },
  onError: (err: any) => {
    createError.value = err?.message ?? t('admin.users.createError')
  },
})

function submitCreate() {
  createError.value = null
  createUser({ ...createForm })
}

// --- CSV import modal ---
const showImportModal = ref(false)
const importFile = ref<File | null>(null)
const importResult = ref<{ created: number; total: number; rows: ImportRow[] } | null>(null)
const importError = ref<string | null>(null)
const importing = ref(false)

async function openImportModal() {
  if (!(await ensureFreshTotp())) return
  importFile.value = null
  importResult.value = null
  importError.value = null
  showImportModal.value = true
}

function handleFileChange(e: Event) {
  const target = e.target as HTMLInputElement
  importFile.value = target.files?.[0] ?? null
}

async function submitImport() {
  if (!importFile.value) return
  importError.value = null
  importing.value = true
  try {
    const fd = new FormData()
    fd.append('file', importFile.value)
    const res = await fetch('/api/v1/admin/users/import', {
      method: 'POST',
      credentials: 'include',
      body: fd,
    })
    if (res.status === 403) {
      const body = await res.json().catch(() => null)
      if (body?.error === 'totp_required') {
        const next = window.location.pathname
        window.location.href = '/admin/verify-totp?next=' + encodeURIComponent(next)
        return
      }
      if (body?.error === 'totp_fresh_required') {
        importError.value = t('admin.users.importFreshRequired')
        return
      }
    }
    if (!res.ok) {
      const body = await res.json().catch(() => null)
      throw new Error(body?.error ?? `HTTP ${res.status}`)
    }
    importResult.value = await res.json()
    queryClient.invalidateQueries({ queryKey: ['admin', 'users'] })
  } catch (e: any) {
    importError.value = e?.message ?? t('admin.users.importError')
  } finally {
    importing.value = false
  }
}
</script>

<template>
  <div>
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.sidebar.users') }}</h1>
      <div class="flex gap-2">
        <button
          class="inline-flex items-center gap-1 rounded-md bg-blue-600 px-3 py-1.5 text-sm font-semibold text-white hover:bg-blue-700"
          @click="openCreateModal"
        >
          <UserPlus class="h-4 w-4" /> {{ t('admin.users.addButton') }}
        </button>
        <button
          class="inline-flex items-center gap-1 rounded-md bg-white px-3 py-1.5 text-sm font-semibold text-gray-700 ring-1 ring-gray-300 hover:bg-gray-50"
          @click="openImportModal"
        >
          <Upload class="h-4 w-4" /> {{ t('admin.users.importButton') }}
        </button>
      </div>
    </div>

    <div v-if="isLoading" class="mt-6 text-gray-500">{{ t('common.loading') }}...</div>
    <div v-else-if="isError" class="mt-6 rounded-md bg-red-50 p-3 text-sm text-red-800">{{ t('admin.users.loadError') }}</div>

    <div v-else class="mt-6 overflow-x-auto">
      <p class="mb-2 text-xs text-gray-500">
        {{ t('admin.users.showing', { from: pageStart, to: pageEnd, total: totalCount }) }}
      </p>
      <table class="min-w-full divide-y divide-gray-200">
        <thead class="bg-gray-50">
          <tr>
            <th scope="col" class="w-12 px-3 py-3 text-right text-xs font-medium uppercase tracking-wider text-gray-500">#</th>
            <th
              scope="col"
              class="cursor-pointer select-none px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500 hover:text-gray-700"
              @click="setSort('first_name')"
            >
              <span class="inline-flex items-center gap-1">
                {{ t('admin.users.firstName') }}
                <ArrowUp v-if="sortField === 'first_name' && sortDir === 'asc'" class="h-3 w-3" />
                <ArrowDown v-else-if="sortField === 'first_name' && sortDir === 'desc'" class="h-3 w-3" />
              </span>
            </th>
            <th
              scope="col"
              class="cursor-pointer select-none px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500 hover:text-gray-700"
              @click="setSort('last_name')"
            >
              <span class="inline-flex items-center gap-1">
                {{ t('admin.users.lastName') }}
                <ArrowUp v-if="sortField === 'last_name' && sortDir === 'asc'" class="h-3 w-3" />
                <ArrowDown v-else-if="sortField === 'last_name' && sortDir === 'desc'" class="h-3 w-3" />
              </span>
            </th>
            <th
              scope="col"
              class="cursor-pointer select-none px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500 hover:text-gray-700"
              @click="setSort('email')"
            >
              <span class="inline-flex items-center gap-1">
                {{ t('admin.users.email') }}
                <ArrowUp v-if="sortField === 'email' && sortDir === 'asc'" class="h-3 w-3" />
                <ArrowDown v-else-if="sortField === 'email' && sortDir === 'desc'" class="h-3 w-3" />
              </span>
            </th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.users.phone') }}</th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.users.roles') }}</th>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('common.actions') }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-200 bg-white">
          <tr
            v-for="(user, index) in users"
            :key="user.id"
            class="cursor-pointer hover:bg-gray-50"
            @click="openDetail(user)"
          >
            <td class="whitespace-nowrap px-3 py-3 text-right text-xs text-gray-400 tabular-nums">{{ offset + index + 1 }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-sm font-medium text-gray-900">{{ user.first_name || formatName(user) }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-sm font-medium text-gray-900">{{ user.last_name }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ user.email }}</td>
            <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ user.phone }}</td>
            <td class="px-4 py-3 text-sm" @click.stop>
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
                  <button class="text-xs text-blue-600 hover:underline" @click="updateRoles({ userId: user.id, roles: editingRoles[user.id] })">{{ t('common.save') }}</button>
                  <button class="text-xs text-gray-500 hover:underline" @click="cancelEditRoles(user.id)">{{ t('common.cancel') }}</button>
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
            <td class="whitespace-nowrap px-4 py-3 text-sm" @click.stop>
              <div class="flex items-center gap-3">
                <button class="text-gray-500 hover:text-blue-700" :title="t('common.edit')" @click="openEditDirect(user)">
                  <Pencil class="h-4 w-4" />
                </button>
                <button class="text-red-600 hover:text-red-800" :title="t('common.delete')" @click="confirmDelete(user.id)">
                  <Trash2 class="h-4 w-4" />
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>

      <div v-if="totalCount > PAGE_SIZE" class="mt-3 flex items-center justify-between text-sm text-gray-600">
        <span>{{ t('admin.users.showing', { from: pageStart, to: pageEnd, total: totalCount }) }}</span>
        <div class="flex gap-2">
          <button
            class="rounded-md px-3 py-1 ring-1 ring-gray-300 hover:bg-gray-50 disabled:opacity-40"
            :disabled="!hasPrev"
            @click="prevPage"
          >{{ t('common.previous') }}</button>
          <button
            class="rounded-md px-3 py-1 ring-1 ring-gray-300 hover:bg-gray-50 disabled:opacity-40"
            :disabled="!hasNext"
            @click="nextPage"
          >{{ t('common.next') }}</button>
        </div>
      </div>
    </div>

    <!-- Detail / edit modal -->
    <div
      v-if="detailUser"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      @click.self="closeDetail"
    >
      <div class="w-full max-w-lg rounded-lg bg-white p-5 shadow-xl">
        <div class="mb-3 flex items-center justify-between">
          <h2 class="text-lg font-semibold text-gray-900">
            {{ detailEditing ? t('admin.users.editTitle') : formatName(detailUser) || detailUser.email }}
          </h2>
          <button class="text-gray-400 hover:text-gray-600" @click="closeDetail">
            <X class="h-5 w-5" />
          </button>
        </div>

        <!-- View mode -->
        <div v-if="!detailEditing" class="space-y-2 text-sm">
          <dl class="grid grid-cols-3 gap-x-3 gap-y-2">
            <dt class="text-xs font-medium text-gray-500">{{ t('admin.users.firstName') }}</dt>
            <dd class="col-span-2 text-gray-900">{{ detailUser.first_name || '—' }}</dd>
            <dt class="text-xs font-medium text-gray-500">{{ t('admin.users.lastName') }}</dt>
            <dd class="col-span-2 text-gray-900">{{ detailUser.last_name || '—' }}</dd>
            <dt class="text-xs font-medium text-gray-500">{{ t('admin.users.email') }}</dt>
            <dd class="col-span-2 text-gray-900">{{ detailUser.email }}</dd>
            <dt class="text-xs font-medium text-gray-500">{{ t('admin.users.phone') }}</dt>
            <dd class="col-span-2 text-gray-900">{{ detailUser.phone || '—' }}</dd>
            <dt class="text-xs font-medium text-gray-500">{{ t('admin.users.address') }}</dt>
            <dd class="col-span-2 text-gray-900">{{ detailUser.address_line || '—' }}</dd>
            <dt class="text-xs font-medium text-gray-500">{{ t('admin.users.postal') }}</dt>
            <dd class="col-span-2 text-gray-900">{{ detailUser.postal_code || '—' }}</dd>
            <dt class="text-xs font-medium text-gray-500">{{ t('admin.users.city') }}</dt>
            <dd class="col-span-2 text-gray-900">{{ detailUser.city || '—' }}</dd>
            <dt class="text-xs font-medium text-gray-500">{{ t('admin.users.isLocal') }}</dt>
            <dd class="col-span-2 text-gray-900">{{ detailUser.is_local ? t('common.yes') : t('common.no') }}</dd>
            <dt class="text-xs font-medium text-gray-500">{{ t('admin.users.roles') }}</dt>
            <dd class="col-span-2 flex flex-wrap gap-1">
              <span v-for="role in detailUser.roles" :key="role" class="rounded-full bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-700">{{ role }}</span>
              <span v-if="!detailUser.roles?.length" class="text-gray-500">—</span>
            </dd>
          </dl>
          <div class="flex justify-end gap-2 pt-3">
            <button class="rounded-md px-3 py-1.5 text-sm text-gray-600 hover:bg-gray-100" @click="closeDetail">{{ t('common.close') }}</button>
            <button
              class="inline-flex items-center gap-1 rounded-md bg-blue-600 px-3 py-1.5 text-sm font-semibold text-white hover:bg-blue-700"
              @click="startDetailEdit"
            >
              <Pencil class="h-4 w-4" /> {{ t('common.edit') }}
            </button>
          </div>
        </div>

        <!-- Edit mode -->
        <form v-else class="space-y-3" @submit.prevent="submitEdit">
          <div class="grid grid-cols-2 gap-2">
            <div>
              <label class="block text-xs font-medium text-gray-700" for="ed-first">{{ t('admin.users.firstName') }}</label>
              <input id="ed-first" v-model="editForm.first_name" type="text" class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm" />
            </div>
            <div>
              <label class="block text-xs font-medium text-gray-700" for="ed-last">{{ t('admin.users.lastName') }}</label>
              <input id="ed-last" v-model="editForm.last_name" type="text" class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm" />
            </div>
          </div>
          <div class="grid grid-cols-2 gap-2">
            <div>
              <label class="block text-xs font-medium text-gray-700" for="ed-phone">{{ t('admin.users.phone') }}</label>
              <input id="ed-phone" v-model="editForm.phone" type="tel" class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm" />
            </div>
            <div class="flex items-end gap-1 pb-1">
              <label class="inline-flex items-center gap-1 text-sm text-gray-700">
                <input v-model="editForm.is_local" type="checkbox" class="rounded border-gray-300" />
                {{ t('admin.users.isLocal') }}
              </label>
            </div>
          </div>
          <div>
            <label class="block text-xs font-medium text-gray-700" for="ed-addr">{{ t('admin.users.address') }}</label>
            <input id="ed-addr" v-model="editForm.address_line" type="text" class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm" />
          </div>
          <div class="grid grid-cols-3 gap-2">
            <div class="col-span-1">
              <label class="block text-xs font-medium text-gray-700" for="ed-postal">{{ t('admin.users.postal') }}</label>
              <input id="ed-postal" v-model="editForm.postal_code" type="text" class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm" />
            </div>
            <div class="col-span-2">
              <label class="block text-xs font-medium text-gray-700" for="ed-city">{{ t('admin.users.city') }}</label>
              <input id="ed-city" v-model="editForm.city" type="text" class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm" />
            </div>
          </div>
          <p v-if="detailError" class="rounded-md bg-red-50 px-2 py-1 text-xs text-red-700">{{ detailError }}</p>
          <div class="flex justify-end gap-2 pt-1">
            <button type="button" class="rounded-md px-3 py-1.5 text-sm text-gray-600 hover:bg-gray-100" @click="detailEditing = false">{{ t('common.cancel') }}</button>
            <button type="submit" :disabled="savingEdit" class="rounded-md bg-blue-600 px-3 py-1.5 text-sm font-semibold text-white hover:bg-blue-700 disabled:opacity-50">
              {{ savingEdit ? t('common.loading') : t('common.save') }}
            </button>
          </div>
        </form>
      </div>
    </div>

    <!-- Create user modal -->
    <div
      v-if="showCreateModal"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      @click.self="showCreateModal = false"
    >
      <div class="w-full max-w-md rounded-lg bg-white p-5 shadow-xl">
        <div class="mb-3 flex items-center justify-between">
          <h2 class="text-lg font-semibold text-gray-900">{{ t('admin.users.addTitle') }}</h2>
          <button class="text-gray-400 hover:text-gray-600" @click="showCreateModal = false">
            <X class="h-5 w-5" />
          </button>
        </div>
        <form class="space-y-3" @submit.prevent="submitCreate">
          <div>
            <label class="block text-xs font-medium text-gray-700" for="cu-email">{{ t('admin.users.email') }} *</label>
            <input id="cu-email" v-model="createForm.email" type="email" required class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm" />
          </div>
          <div class="grid grid-cols-2 gap-2">
            <div>
              <label class="block text-xs font-medium text-gray-700" for="cu-first">{{ t('admin.users.firstName') }} *</label>
              <input id="cu-first" v-model="createForm.first_name" type="text" required class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm" />
            </div>
            <div>
              <label class="block text-xs font-medium text-gray-700" for="cu-last">{{ t('admin.users.lastName') }}</label>
              <input id="cu-last" v-model="createForm.last_name" type="text" class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm" />
            </div>
          </div>
          <div class="grid grid-cols-2 gap-2">
            <div>
              <label class="block text-xs font-medium text-gray-700" for="cu-phone">{{ t('admin.users.phone') }}</label>
              <input id="cu-phone" v-model="createForm.phone" type="tel" class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm" />
            </div>
            <div class="flex items-end gap-1 pb-1">
              <label class="inline-flex items-center gap-1 text-sm text-gray-700">
                <input v-model="createForm.is_local" type="checkbox" class="rounded border-gray-300" />
                {{ t('admin.users.isLocal') }}
              </label>
            </div>
          </div>
          <div>
            <label class="block text-xs font-medium text-gray-700" for="cu-addr">{{ t('admin.users.address') }}</label>
            <input id="cu-addr" v-model="createForm.address_line" type="text" class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm" />
          </div>
          <div class="grid grid-cols-3 gap-2">
            <div class="col-span-1">
              <label class="block text-xs font-medium text-gray-700" for="cu-postal">{{ t('admin.users.postal') }}</label>
              <input id="cu-postal" v-model="createForm.postal_code" type="text" class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm" />
            </div>
            <div class="col-span-2">
              <label class="block text-xs font-medium text-gray-700" for="cu-city">{{ t('admin.users.city') }}</label>
              <input id="cu-city" v-model="createForm.city" type="text" class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm" />
            </div>
          </div>
          <div>
            <span class="block text-xs font-medium text-gray-700">{{ t('admin.users.roles') }}</span>
            <div class="mt-1 flex flex-wrap gap-1">
              <button
                v-for="role in allRoles"
                :key="role"
                type="button"
                :class="[
                  'rounded-full px-2 py-0.5 text-xs font-medium transition',
                  createForm.roles.includes(role)
                    ? 'bg-blue-600 text-white'
                    : 'bg-gray-100 text-gray-600 hover:bg-gray-200',
                ]"
                @click="toggleCreateRole(role)"
              >
                {{ role }}
              </button>
            </div>
          </div>
          <p v-if="createError" class="rounded-md bg-red-50 px-2 py-1 text-xs text-red-700">{{ createError }}</p>
          <div class="flex justify-end gap-2 pt-1">
            <button type="button" class="rounded-md px-3 py-1.5 text-sm text-gray-600 hover:bg-gray-100" @click="showCreateModal = false">{{ t('common.cancel') }}</button>
            <button type="submit" :disabled="creating" class="rounded-md bg-blue-600 px-3 py-1.5 text-sm font-semibold text-white hover:bg-blue-700 disabled:opacity-50">
              {{ creating ? t('common.loading') : t('admin.users.addSubmit') }}
            </button>
          </div>
        </form>
      </div>
    </div>

    <!-- CSV import modal -->
    <div
      v-if="showImportModal"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      @click.self="showImportModal = false"
    >
      <div class="w-full max-w-2xl rounded-lg bg-white p-5 shadow-xl">
        <div class="mb-3 flex items-center justify-between">
          <h2 class="text-lg font-semibold text-gray-900">{{ t('admin.users.importTitle') }}</h2>
          <button class="text-gray-400 hover:text-gray-600" @click="showImportModal = false">
            <X class="h-5 w-5" />
          </button>
        </div>

        <div v-if="!importResult">
          <p class="text-sm text-gray-600">{{ t('admin.users.importDescription') }}</p>
          <pre class="mt-2 overflow-x-auto rounded bg-gray-50 p-2 text-xs text-gray-700">email,first_name,last_name,phone,address_line,postal_code,city,is_local,roles
ada@example.com,Ada,Lovelace,,,,,,member;board
grace@example.com,Grace,Hopper,+47 555 1234,,,,true,member</pre>
          <input type="file" accept=".csv,text/csv" class="mt-3 block text-sm" @change="handleFileChange" />
          <p v-if="importError" class="mt-2 rounded-md bg-red-50 px-2 py-1 text-xs text-red-700">{{ importError }}</p>
          <div class="mt-3 flex justify-end gap-2">
            <button type="button" class="rounded-md px-3 py-1.5 text-sm text-gray-600 hover:bg-gray-100" @click="showImportModal = false">{{ t('common.cancel') }}</button>
            <button type="button" :disabled="!importFile || importing" class="rounded-md bg-blue-600 px-3 py-1.5 text-sm font-semibold text-white hover:bg-blue-700 disabled:opacity-50" @click="submitImport">
              {{ importing ? t('common.loading') : t('admin.users.importSubmit') }}
            </button>
          </div>
        </div>

        <div v-else>
          <p class="rounded-md bg-green-50 px-3 py-2 text-sm text-green-800">
            {{ t('admin.users.importSummary', { created: importResult.created, total: importResult.total }) }}
          </p>
          <div class="mt-3 max-h-80 overflow-y-auto">
            <table class="min-w-full text-xs">
              <thead class="bg-gray-50">
                <tr>
                  <th class="px-2 py-1 text-left font-medium text-gray-600">#</th>
                  <th class="px-2 py-1 text-left font-medium text-gray-600">{{ t('admin.users.email') }}</th>
                  <th class="px-2 py-1 text-left font-medium text-gray-600">{{ t('admin.users.importStatus') }}</th>
                  <th class="px-2 py-1 text-left font-medium text-gray-600">{{ t('admin.users.importDetail') }}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100">
                <tr v-for="row in importResult.rows" :key="row.row">
                  <td class="px-2 py-1 text-gray-500">{{ row.row }}</td>
                  <td class="px-2 py-1 text-gray-700">{{ row.email }}</td>
                  <td class="px-2 py-1">
                    <span :class="{
                      'text-green-700': row.status === 'created',
                      'text-amber-700': row.status === 'skipped',
                      'text-red-700': row.status === 'error',
                    }">{{ row.status }}</span>
                  </td>
                  <td class="px-2 py-1 text-gray-500">{{ row.error }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <div class="mt-3 flex justify-end">
            <button type="button" class="rounded-md bg-blue-600 px-3 py-1.5 text-sm font-semibold text-white hover:bg-blue-700" @click="showImportModal = false">{{ t('common.close') }}</button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
