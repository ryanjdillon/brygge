<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Search, X } from 'lucide-vue-next'

export interface MemberHit {
  id: string
  full_name: string
  email: string
  address_line?: string
  postal_code?: string
  city?: string
}

const props = defineProps<{
  /** Currently-selected member, if any. Undefined while searching. */
  modelValue: MemberHit | null
  placeholder?: string
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: MemberHit | null): void
}>()

const { t } = useI18n()
const query = ref('')
const results = ref<MemberHit[]>([])
const showResults = ref(false)
const loading = ref(false)
let debounce: ReturnType<typeof setTimeout> | null = null

watch(query, (q) => {
  if (debounce) clearTimeout(debounce)
  if (q.trim().length < 2) {
    results.value = []
    showResults.value = false
    return
  }
  debounce = setTimeout(async () => {
    loading.value = true
    try {
      const url = `/api/v1/admin/users?q=${encodeURIComponent(q.trim())}&limit=20`
      const res = await fetch(url, { credentials: 'include' })
      if (!res.ok) {
        results.value = []
        return
      }
      const body = await res.json()
      const items: MemberHit[] = (body.items ?? body.users ?? body ?? []).map((u: Record<string, unknown>) => ({
        id: String(u.id),
        full_name: String(u.full_name ?? `${u.first_name ?? ''} ${u.last_name ?? ''}`).trim(),
        email: String(u.email ?? ''),
        address_line: u.address_line ? String(u.address_line) : undefined,
        postal_code: u.postal_code ? String(u.postal_code) : undefined,
        city: u.city ? String(u.city) : undefined,
      }))
      results.value = items
      showResults.value = items.length > 0
    } finally {
      loading.value = false
    }
  }, 250)
})

function pick(m: MemberHit) {
  emit('update:modelValue', m)
  query.value = ''
  showResults.value = false
}

function clearSelection() {
  emit('update:modelValue', null)
}

function delayHide() {
  setTimeout(() => (showResults.value = false), 200)
}
</script>

<template>
  <div class="relative">
    <div v-if="props.modelValue" class="flex items-center justify-between rounded-md border border-gray-300 bg-blue-50 px-3 py-2">
      <div>
        <p class="text-sm font-medium text-blue-900">{{ props.modelValue.full_name }}</p>
        <p class="text-xs text-blue-700">{{ props.modelValue.email }}</p>
      </div>
      <button type="button" class="text-blue-700 hover:text-blue-900" :title="t('common.cancel')" @click="clearSelection">
        <X class="h-4 w-4" />
      </button>
    </div>
    <template v-else>
      <div class="relative">
        <Search class="pointer-events-none absolute left-3 top-2.5 h-4 w-4 text-gray-400" />
        <input
          v-model="query"
          type="search"
          :placeholder="props.placeholder ?? t('admin.memberSearch.placeholder')"
          class="block w-full rounded-md border border-gray-300 py-2 pl-9 pr-3 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          @focus="showResults = results.length > 0"
          @blur="delayHide"
        />
      </div>
      <div
        v-if="showResults"
        class="absolute z-10 mt-1 max-h-72 w-full overflow-auto rounded-md border border-gray-200 bg-white shadow-lg"
      >
        <button
          v-for="m in results"
          :key="m.id"
          type="button"
          class="block w-full px-4 py-2 text-left text-sm hover:bg-blue-50"
          @mousedown.prevent="pick(m)"
        >
          <span class="block font-medium text-gray-900">{{ m.full_name }}</span>
          <span class="block text-xs text-gray-500">{{ m.email }}</span>
        </button>
      </div>
      <p v-if="loading" class="mt-1 text-xs text-gray-400">{{ t('common.loading') }}…</p>
    </template>
  </div>
</template>
