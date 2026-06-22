<script setup lang="ts" generic="T extends string | number">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { ChevronDown, X } from 'lucide-vue-next'

export interface MultiSelectOption<V> {
  value: V
  label: string
  disabled?: boolean
}

const props = withDefaults(
  defineProps<{
    modelValue: T[]
    options: MultiSelectOption<T>[]
    placeholder?: string
    disabled?: boolean
    width?: 'full' | 'content'
    ariaLabel?: string
  }>(),
  { width: 'full', placeholder: '—' },
)

const emit = defineEmits<{
  (e: 'update:modelValue', value: T[]): void
}>()

const open = ref(false)
const root = ref<HTMLDivElement | null>(null)

const selectedSet = computed(() => new Set(props.modelValue))
const selectedLabels = computed(() =>
  props.options.filter((o) => selectedSet.value.has(o.value)).map((o) => o.label),
)

function toggle(value: T) {
  const set = new Set(props.modelValue)
  if (set.has(value)) set.delete(value)
  else set.add(value)
  emit('update:modelValue', [...set])
}

function clear() {
  emit('update:modelValue', [])
}

function onDocClick(e: MouseEvent) {
  if (!root.value) return
  if (!root.value.contains(e.target as Node)) open.value = false
}

onMounted(() => document.addEventListener('mousedown', onDocClick))
onBeforeUnmount(() => document.removeEventListener('mousedown', onDocClick))

const widthClass = computed(() => (props.width === 'content' ? 'inline-block' : 'block w-full'))
const triggerWidthClass = computed(() => (props.width === 'content' ? 'w-auto' : 'w-full'))
</script>

<template>
  <div ref="root" :class="['relative', widthClass]">
    <button
      type="button"
      class="flex items-center justify-between gap-2 rounded-md border border-gray-300 bg-white px-3 py-1.5 text-left text-sm hover:border-gray-400 focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500 disabled:cursor-not-allowed disabled:bg-gray-50"
      :class="triggerWidthClass"
      :disabled="disabled"
      :aria-expanded="open"
      :aria-haspopup="'listbox'"
      :aria-label="ariaLabel"
      @click="open = !open"
    >
      <span v-if="selectedLabels.length" class="truncate text-gray-800">{{ selectedLabels.join(', ') }}</span>
      <span v-else class="truncate text-gray-400">{{ placeholder }}</span>
      <span class="flex flex-shrink-0 items-center gap-1">
        <button
          v-if="selectedLabels.length"
          type="button"
          class="rounded p-0.5 hover:bg-gray-100"
          :aria-label="'Clear'"
          @click.stop="clear"
        >
          <X class="h-3 w-3 text-gray-500" />
        </button>
        <ChevronDown class="h-4 w-4 text-gray-500" />
      </span>
    </button>

    <div
      v-if="open"
      class="absolute z-20 mt-1 max-h-72 min-w-full overflow-y-auto rounded-md border border-gray-200 bg-white py-1 shadow-lg"
      role="listbox"
    >
      <button
        v-for="o in options"
        :key="String(o.value)"
        type="button"
        class="flex w-full items-center gap-2 whitespace-nowrap px-3 py-1.5 text-left text-sm hover:bg-brand-50 disabled:cursor-not-allowed disabled:opacity-50"
        :class="{ 'bg-brand-50': selectedSet.has(o.value) }"
        :disabled="o.disabled"
        role="option"
        :aria-selected="selectedSet.has(o.value)"
        @click="toggle(o.value)"
      >
        <input type="checkbox" :checked="selectedSet.has(o.value)" class="h-3.5 w-3.5" tabindex="-1" />
        <span class="text-gray-800">{{ o.label }}</span>
      </button>
      <p v-if="options.length === 0" class="px-3 py-2 text-xs text-gray-500">—</p>
    </div>
  </div>
</template>
