<script setup lang="ts">
import { ref, computed, reactive, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'
import { sortBySlip } from '@/lib/slipSort'
import DockFilter from '@/components/admin/DockFilter.vue'
import SortableTh from '@/components/admin/SortableTh.vue'
import SlipCell from '@/components/admin/SlipCell.vue'
import { Trash2, UserPlus, Upload, X, Pencil } from 'lucide-vue-next'
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

type SortField = 'first_name' | 'last_name' | 'email' | 'slip'
const sortField = ref<SortField>('last_name')
const sortDir = ref<'asc' | 'desc'>('asc')
const sortParam = computed(() => (sortDir.value === 'desc' ? '-' : '') + sortField.value)

type SpotFilter = '' | 'permanent' | 'seasonal' | 'none'
const spotFilter = ref<SpotFilter>('')
const dockFilter = ref<string>('')

const PAGE_SIZE = 100
const offset = ref(0)

// Two-stage search: `searchInput` mirrors the textbox for instant
// keystroke feedback; `searchQuery` is the value actually sent to the
// API, debounced 250ms so we don't burn a request per keystroke.
const searchInput = ref('')
const searchQuery = ref('')
let searchDebounce: ReturnType<typeof setTimeout> | null = null
function onSearchInput() {
  if (searchDebounce) clearTimeout(searchDebounce)
  searchDebounce = setTimeout(() => {
    searchQuery.value = searchInput.value.trim()
    offset.value = 0
  }, 250)
}

const { data: usersResponse, isLoading, isError } = useQuery({
  queryKey: ['admin', 'users', sortParam, spotFilter, dockFilter, searchQuery, offset],
  queryFn: async () =>
    unwrap(
      await client.GET('/api/v1/admin/users', {
        params: {
          query: {
            limit: PAGE_SIZE,
            offset: offset.value,
            sort: sortParam.value,
            ...(spotFilter.value ? { spot: spotFilter.value } : {}),
            ...(dockFilter.value ? { dock: dockFilter.value } : {}),
            ...(searchQuery.value ? { q: searchQuery.value } : {}),
          } as any,
        },
      }),
    ),
})

function onSpotFilterChange() {
  offset.value = 0
}

function onDockFilterChange() {
  offset.value = 0
}

// Slip picker — fetched once per modal-open, lightweight raw fetch since
// the openapi-fetch typing for /admin/slips wraps the row shape in a
// PaginatedResponse the codegen doesn't fully express.
type SlipOption = {
  id: string
  number: string
  section: string
  status: string
  occupant_id: string | null
}
const slipOptions = ref<SlipOption[]>([])
const slipsLoading = ref(false)
// Eagerly preload the slip list on mount so the dock-filter dropdown
// has options to render. Cheap (~hundreds of rows max) and the same
// data feeds the slip-picker inside the user-edit modal.
onMounted(() => { loadSlips({ force: false }) })

const dockOptions = computed<string[]>(() => {
  const set = new Set<string>()
  for (const s of slipOptions.value) {
    if (s.section) set.add(s.section)
  }
  return [...set].sort((a, b) => a.localeCompare(b, undefined, { numeric: true }))
})

// Always refetch — occupancy changes as slips get assigned/released
// elsewhere (this session, another tab, another admin), and a stale
// cache here would show occupied slips as available in the picker.
// Caller can pass { force: false } to opt back into the one-shot cache
// for the dock-filter dropdown's mount-time prefetch.
async function loadSlips({ force = true }: { force?: boolean } = {}) {
  if (!force && slipOptions.value.length > 0) return
  slipsLoading.value = true
  try {
    const res = await fetch('/api/v1/admin/slips?limit=500', { credentials: 'include' })
    if (!res.ok) return
    const body = await res.json()
    slipOptions.value = (body.items ?? body.data ?? []).map((s: any) => ({
      id: s.id,
      number: s.number,
      section: s.section,
      status: s.status,
      occupant_id: s.occupant_id ?? null,
    }))
  } finally {
    slipsLoading.value = false
  }
}

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

const allRoles = [
  'member', 'slip_holder', 'board', 'harbor_master', 'treasurer', 'admin',
  'chair', 'vice_chair', 'deputy', 'secretary',
]

// roleLabel returns the localized display name for a role identifier,
// falling back to the raw key if no translation is registered (so new
// roles added on the backend don't blow up the UI before locales catch
// up).
function roleLabel(role: string): string {
  const key = `admin.users.role.${role}`
  const label = t(key)
  return label === key ? role : label
}

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
const editRoles = ref<string[]>([])
const editSlipId = ref<string>('')
const editSlipType = ref<'permanent' | 'seasonal'>('permanent')
const savingEdit = ref(false)

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
    email: u.email ?? '',
    first_name: u.first_name ?? '',
    last_name: u.last_name ?? '',
    phone: u.phone ?? '',
    address_line: u.address_line ?? '',
    postal_code: u.postal_code ?? '',
    city: u.city ?? '',
    is_local: !!u.is_local,
  })
  editRoles.value = [...(u.roles ?? [])]
  editSlipId.value = u.slip_id ?? ''
  editSlipType.value = (u.slip_assignment_type === 'seasonal' ? 'seasonal' : 'permanent')
  detailError.value = null
  detailEditing.value = true
  loadSlips()
}

async function openEditDirect(user: User) {
  openDetail(user)
  await startDetailEdit()
}

function toggleEditRole(role: string) {
  const idx = editRoles.value.indexOf(role)
  if (idx >= 0) editRoles.value.splice(idx, 1)
  else editRoles.value.push(role)
}

async function submitEdit() {
  if (!detailUser.value) return
  const u = detailUser.value
  detailError.value = null
  savingEdit.value = true
  try {
    await unwrap(await client.PATCH('/api/v1/admin/users/{userID}', {
      params: { path: { userID: u.id } },
      body: { ...editForm },
    }))

    // Roles diff — call the dedicated endpoint only if the set changed.
    const before = new Set(u.roles ?? [])
    const after = new Set(editRoles.value)
    const sameRoles = before.size === after.size && [...before].every((r) => after.has(r))
    if (!sameRoles) {
      await unwrap(await client.PUT('/api/v1/admin/users/{userID}/roles', {
        params: { path: { userID: u.id } },
        body: { roles: editRoles.value } as any,
      }))
    }

    // Slip diff — release/assign through the new endpoint when the
    // selection or type has changed.
    const slipChanged = (u.slip_id ?? '') !== editSlipId.value
    const typeChanged = !!editSlipId.value && (u.slip_assignment_type || 'permanent') !== editSlipType.value
    if (slipChanged || typeChanged) {
      await unwrap(await client.PUT('/api/v1/admin/users/{userID}/slip', {
        params: { path: { userID: u.id } },
        body: {
          slip_id: editSlipId.value || null,
          assignment_type: editSlipType.value,
        } as any,
      }))
    }

    detailEditing.value = false
    closeDetail()
    queryClient.invalidateQueries({ queryKey: ['admin', 'users'] })
    // Slip occupancy may have flipped — refresh the cached picker list
    // so the next edit sees the new state.
    if (slipChanged || typeChanged) loadSlips()
  } catch (err: any) {
    detailError.value = err?.message ?? t('admin.users.updateError')
  } finally {
    savingEdit.value = false
  }
}

// Filter slip-picker options to: vacant slips OR the user's currently
// assigned slip (so it stays selectable in the dropdown). Sort by dock
// (section) then slip number using natural-numeric collation so A2 sorts
// before A10.
// Occupancy is derived from active slip_assignments (occupant_id),
// not the slips.status column — the latter isn't updated automatically
// when an assignment is created/released, so a freshly-assigned slip
// could still read status='vacant'. Trust the live join instead.
const slipPickerOptions = computed(() => {
  const currentUserId = detailUser.value?.id ?? ''
  const currentSlipId = detailUser.value?.slip_id ?? ''
  return sortBySlip(
    slipOptions.value.filter((s) => {
      // Always keep this user's own current slip so it stays selectable.
      if (s.id === currentSlipId) return true
      // Otherwise only show slips with no active assignment, or whose
      // active assignment somehow already points at this user.
      return !s.occupant_id || s.occupant_id === currentUserId
    }),
  )
})

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
      <div class="flex flex-wrap items-center gap-2">
        <label class="sr-only" for="user-search">{{ t('admin.users.searchPlaceholder') }}</label>
        <input
          id="user-search"
          v-model="searchInput"
          type="search"
          :placeholder="t('admin.users.searchPlaceholder')"
          class="rounded-md border border-gray-300 bg-white px-2 py-1.5 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          @input="onSearchInput"
        />
        <DockFilter
          id="member-dock-filter"
          v-model="dockFilter"
          :options="dockOptions"
          @update:model-value="onDockFilterChange"
        />
        <label class="sr-only" for="spot-filter">{{ t('admin.users.spotFilterLabel') }}</label>
        <select
          id="spot-filter"
          v-model="spotFilter"
          class="rounded-md border border-gray-300 bg-white px-2 py-1.5 text-sm"
          :title="t('admin.users.spotFilterLabel')"
          @change="onSpotFilterChange"
        >
          <option value="">{{ t('admin.users.spotFilterLabel') }}: {{ t('admin.users.spotFilterAll') }}</option>
          <option value="permanent">{{ t('admin.users.spotFilterLabel') }}: {{ t('admin.users.spotPermanent') }}</option>
          <option value="seasonal">{{ t('admin.users.spotFilterLabel') }}: {{ t('admin.users.spotSeasonal') }}</option>
          <option value="none">{{ t('admin.users.spotFilterLabel') }}: {{ t('admin.users.spotNone') }}</option>
        </select>
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

    <div v-else class="mt-4 overflow-x-auto">
      <p class="mb-2 text-xs text-gray-500">
        {{ t('admin.users.showing', { from: pageStart, to: pageEnd, total: totalCount }) }}
      </p>
      <table class="min-w-full divide-y divide-gray-200">
        <thead class="bg-gray-50">
          <tr>
            <th scope="col" class="w-12 px-3 py-3 text-right text-xs font-medium uppercase tracking-wider text-gray-500">#</th>
            <SortableTh :active="sortField === 'first_name'" :dir="sortDir" @click="setSort('first_name')">{{ t('admin.users.firstName') }}</SortableTh>
            <SortableTh :active="sortField === 'last_name'" :dir="sortDir" @click="setSort('last_name')">{{ t('admin.users.lastName') }}</SortableTh>
            <SortableTh :active="sortField === 'email'" :dir="sortDir" @click="setSort('email')">{{ t('admin.users.email') }}</SortableTh>
            <th scope="col" class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.users.phone') }}</th>
            <SortableTh :active="sortField === 'slip'" :dir="sortDir" @click="setSort('slip')">{{ t('admin.users.spot') }}</SortableTh>
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
            <td class="whitespace-nowrap px-4 py-3 text-sm">
              <template v-if="user.slip_id">
                <SlipCell :section="user.slip_section" :number="user.slip_number" />
                <span
                  :class="[
                    'ml-1 rounded-full px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide',
                    user.slip_assignment_type === 'seasonal'
                      ? 'bg-amber-100 text-amber-800'
                      : 'bg-emerald-100 text-emerald-800',
                  ]"
                >{{ t('admin.users.spot' + (user.slip_assignment_type === 'seasonal' ? 'Seasonal' : 'Permanent')) }}</span>
              </template>
              <SlipCell v-else />
            </td>
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
                    {{ roleLabel(role) }}
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
                    {{ roleLabel(role) }}
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
            <dt class="text-xs font-medium text-gray-500">{{ t('admin.users.spot') }}</dt>
            <dd class="col-span-2 text-gray-900">
              <template v-if="detailUser.slip_id">
                {{ detailUser.slip_section ? detailUser.slip_section + ' ' : '' }}{{ detailUser.slip_number }}
                <span class="ml-1 text-xs text-gray-500">({{ t('admin.users.spot' + (detailUser.slip_assignment_type === 'seasonal' ? 'Seasonal' : 'Permanent')) }})</span>
              </template>
              <span v-else>—</span>
            </dd>
            <dt class="text-xs font-medium text-gray-500">{{ t('admin.users.roles') }}</dt>
            <dd class="col-span-2 flex flex-wrap gap-1">
              <span v-for="role in detailUser.roles" :key="role" class="rounded-full bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-700">{{ roleLabel(role) }}</span>
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
          <div>
            <label class="block text-xs font-medium text-gray-700" for="ed-email">{{ t('admin.users.email') }}</label>
            <input id="ed-email" v-model="editForm.email" type="email" required class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm" />
          </div>
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
          <div>
            <span class="block text-xs font-medium text-gray-700">{{ t('admin.users.roles') }}</span>
            <div class="mt-1 flex flex-wrap gap-1">
              <button
                v-for="role in allRoles"
                :key="role"
                type="button"
                :class="[
                  'rounded-full px-2 py-0.5 text-xs font-medium transition',
                  editRoles.includes(role)
                    ? 'bg-blue-600 text-white'
                    : 'bg-gray-100 text-gray-600 hover:bg-gray-200',
                ]"
                @click="toggleEditRole(role)"
              >{{ roleLabel(role) }}</button>
            </div>
          </div>
          <div class="grid grid-cols-3 gap-2">
            <div class="col-span-2">
              <label class="block text-xs font-medium text-gray-700" for="ed-slip">{{ t('admin.users.spot') }}</label>
              <select
                id="ed-slip"
                v-model="editSlipId"
                class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm"
                :disabled="slipsLoading"
              >
                <option value="">{{ t('admin.users.spotNone') }}</option>
                <option v-for="s in slipPickerOptions" :key="s.id" :value="s.id">
                  {{ (s.section ? s.section + ' ' : '') + s.number }}
                </option>
              </select>
            </div>
            <div class="col-span-1">
              <label class="block text-xs font-medium text-gray-700" for="ed-slip-type">{{ t('admin.users.spotType') }}</label>
              <select
                id="ed-slip-type"
                v-model="editSlipType"
                :disabled="!editSlipId"
                class="mt-1 w-full rounded-md border border-gray-300 px-2 py-1 text-sm disabled:bg-gray-100"
              >
                <option value="permanent">{{ t('admin.users.spotPermanent') }}</option>
                <option value="seasonal">{{ t('admin.users.spotSeasonal') }}</option>
              </select>
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
                {{ roleLabel(role) }}
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
