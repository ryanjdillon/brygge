<script setup lang="ts" generic="T extends string | number">
import { computed, ref } from 'vue'
import { Search, X } from 'lucide-vue-next'

interface Option {
  value: T
  label: string
  hint?: string
  meta?: string
  disabled?: boolean
}

const props = withDefaults(
  defineProps<{
    modelValue: T[]
    options: Option[]
    placeholder?: string
    emptyText?: string
    searchable?: boolean
    maxHeightClass?: string
  }>(),
  {
    placeholder: 'Search…',
    emptyText: 'No options',
    searchable: true,
    maxHeightClass: 'max-h-56',
  },
)

const emit = defineEmits<{
  (e: 'update:modelValue', value: T[]): void
}>()

const query = ref('')
const filtered = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return props.options
  return props.options.filter(
    (o) =>
      o.label.toLowerCase().includes(q) ||
      (o.hint ?? '').toLowerCase().includes(q) ||
      (o.meta ?? '').toLowerCase().includes(q),
  )
})

const selectedSet = computed(() => new Set<T>(props.modelValue))
const allFilteredSelected = computed(
  () => filtered.value.length > 0 && filtered.value.every((o) => selectedSet.value.has(o.value)),
)

function toggle(value: T, disabled?: boolean) {
  if (disabled) return
  const next = new Set(props.modelValue)
  if (next.has(value)) next.delete(value)
  else next.add(value)
  emit('update:modelValue', [...next])
}

function toggleAll() {
  if (allFilteredSelected.value) {
    const drop = new Set(filtered.value.map((o) => o.value))
    emit(
      'update:modelValue',
      props.modelValue.filter((v) => !drop.has(v)),
    )
  } else {
    const next = new Set(props.modelValue)
    for (const o of filtered.value) if (!o.disabled) next.add(o.value)
    emit('update:modelValue', [...next])
  }
}

function clearSearch() {
  query.value = ''
}
</script>

<template>
  <div class="rounded-md border border-gray-200 bg-white">
    <div v-if="searchable" class="flex items-center gap-2 border-b border-gray-200 px-2 py-1.5">
      <Search class="h-4 w-4 text-gray-400" />
      <input
        v-model="query"
        type="search"
        :placeholder="placeholder"
        class="w-full bg-transparent text-sm outline-none placeholder:text-gray-400"
      />
      <button
        v-if="query"
        type="button"
        class="text-gray-400 hover:text-gray-600"
        @click="clearSearch"
      >
        <X class="h-3.5 w-3.5" />
      </button>
      <button
        v-if="filtered.length"
        type="button"
        class="ml-1 whitespace-nowrap rounded px-1.5 py-0.5 text-[11px] font-medium text-brand-700 hover:bg-brand-50"
        @click="toggleAll"
      >
        {{ allFilteredSelected ? 'Clear' : 'All' }}
      </button>
    </div>

    <ul :class="['divide-y divide-gray-100 overflow-y-auto', maxHeightClass]">
      <li v-if="filtered.length === 0" class="px-3 py-3 text-center text-xs text-gray-400">
        {{ emptyText }}
      </li>
      <li v-for="o in filtered" :key="String(o.value)">
        <label
          :class="[
            'flex cursor-pointer items-start gap-2 px-3 py-2 text-sm',
            o.disabled ? 'cursor-not-allowed opacity-50' : 'hover:bg-brand-50/50',
            selectedSet.has(o.value) && !o.disabled ? 'bg-brand-50/40' : '',
          ]"
        >
          <input
            type="checkbox"
            class="mt-0.5 h-4 w-4 rounded border-gray-300 text-brand-600 focus:ring-brand-500"
            :checked="selectedSet.has(o.value)"
            :disabled="o.disabled"
            @change="toggle(o.value, o.disabled)"
          />
          <div class="min-w-0 flex-1">
            <div class="flex items-baseline justify-between gap-2">
              <span class="truncate font-medium text-gray-900">{{ o.label }}</span>
              <span v-if="o.meta" class="shrink-0 text-xs text-gray-500">{{ o.meta }}</span>
            </div>
            <p v-if="o.hint" class="truncate text-xs text-gray-500">{{ o.hint }}</p>
          </div>
        </label>
      </li>
    </ul>
  </div>
</template>
