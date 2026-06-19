<script setup lang="ts">
import { ref, watch } from 'vue'
import { X } from 'lucide-vue-next'
import { useApiClient, unwrap } from '@/lib/apiClient'
import type { components } from '@/types/api.d.ts'

export interface RecipientValue {
  groups: string[]
  individuals: { name: string; email: string }[]
}

const props = defineProps<{
  modelValue: RecipientValue
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', v: RecipientValue): void
}>()

const client = useApiClient()

type AdminUser = components['schemas']['AdminUser']

const GROUP_OPTS = [
  { value: 'all',          label: 'Alle' },
  { value: 'members',      label: 'Medlemar' },
  { value: 'board',        label: 'Styremedlemar' },
  { value: 'slip_holders', label: 'Plasseigarar' },
  { value: 'waiting_list', label: 'Venteliste' },
]

const searchTerm = ref('')
const searchResults = ref<AdminUser[]>([])
const showDropdown = ref(false)
let debounceTimer: ReturnType<typeof setTimeout> | null = null

function toggleGroup(g: string) {
  let groups = [...props.modelValue.groups]
  if (g === 'all') {
    groups = groups.includes('all') ? [] : ['all']
  } else {
    const allIdx = groups.indexOf('all')
    if (allIdx >= 0) groups.splice(allIdx, 1)
    const idx = groups.indexOf(g)
    if (idx >= 0) groups.splice(idx, 1)
    else groups.push(g)
  }
  emit('update:modelValue', { ...props.modelValue, groups })
}

function removeIndividual(email: string) {
  emit('update:modelValue', {
    ...props.modelValue,
    individuals: props.modelValue.individuals.filter((i) => i.email !== email),
  })
}

function addIndividual(user: AdminUser) {
  if (!user.email) return
  if (props.modelValue.individuals.some((i) => i.email === user.email)) return
  emit('update:modelValue', {
    ...props.modelValue,
    individuals: [
      ...props.modelValue.individuals,
      { name: user.full_name ?? '', email: user.email },
    ],
  })
  searchTerm.value = ''
  searchResults.value = []
  showDropdown.value = false
}

watch(searchTerm, (val) => {
  if (debounceTimer) clearTimeout(debounceTimer)
  if (!val.trim()) {
    searchResults.value = []
    showDropdown.value = false
    return
  }
  debounceTimer = setTimeout(async () => {
    try {
      const res = await unwrap(
        await client.GET('/api/v1/admin/users', {
          params: { query: { q: val, limit: 10 } as any },
        }),
      )
      const picked = new Set(props.modelValue.individuals.map((i) => i.email))
      searchResults.value = (res?.users ?? []).filter(
        (u: AdminUser) => u.email && !picked.has(u.email),
      )
      showDropdown.value = searchResults.value.length > 0
    } catch {
      searchResults.value = []
      showDropdown.value = false
    }
  }, 250)
})

function onSearchBlur() {
  setTimeout(() => { showDropdown.value = false }, 150)
}
</script>

<template>
  <div class="space-y-3">
    <!-- Group toggle chips -->
    <div class="flex flex-wrap gap-2">
      <button
        v-for="opt in GROUP_OPTS"
        :key="opt.value"
        type="button"
        :class="[
          'rounded-full border px-3 py-1 text-sm font-medium transition',
          modelValue.groups.includes(opt.value)
            ? 'border-blue-600 bg-blue-600 text-white'
            : 'border-gray-300 bg-white text-gray-700 hover:border-gray-400',
        ]"
        @click="toggleGroup(opt.value)"
      >
        {{ opt.label }}
      </button>
    </div>

    <!-- Selected individuals -->
    <div v-if="modelValue.individuals.length" class="flex flex-wrap gap-1.5">
      <span
        v-for="ind in modelValue.individuals"
        :key="ind.email"
        class="inline-flex items-center gap-1 rounded-full bg-gray-100 pl-2.5 pr-1.5 py-0.5 text-sm text-gray-800"
      >
        <span class="max-w-[180px] truncate">{{ ind.name || ind.email }}</span>
        <button
          type="button"
          class="rounded-full p-0.5 hover:bg-gray-300"
          @click="removeIndividual(ind.email)"
        >
          <X class="h-3 w-3" />
        </button>
      </span>
    </div>

    <!-- Individual member search -->
    <div class="relative">
      <input
        v-model="searchTerm"
        type="text"
        placeholder="Søk enkeltpersonar…"
        class="w-full rounded-md border border-gray-300 px-3 py-1.5 text-sm placeholder-gray-400 focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        @blur="onSearchBlur"
      />
      <ul
        v-if="showDropdown"
        class="absolute z-20 mt-1 w-full rounded-md border border-gray-200 bg-white py-1 shadow-lg"
      >
        <li
          v-for="user in searchResults"
          :key="user.id"
          class="flex cursor-pointer flex-col px-3 py-2 hover:bg-blue-50"
          @mousedown.prevent="addIndividual(user)"
        >
          <span class="text-sm font-medium text-gray-900">{{ user.full_name }}</span>
          <span class="text-xs text-gray-500">{{ user.email }}</span>
        </li>
      </ul>
    </div>
  </div>
</template>
