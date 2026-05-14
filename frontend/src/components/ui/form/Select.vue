<script setup lang="ts" generic="T extends string | number">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { ChevronDown } from 'lucide-vue-next'

export interface SelectOption<V> {
  value: V
  label: string
  disabled?: boolean
}

const props = withDefaults(
  defineProps<{
    modelValue: T | null | undefined
    options: SelectOption<T>[]
    placeholder?: string
    disabled?: boolean
    required?: boolean
    width?: 'full' | 'content'
    ariaLabel?: string
  }>(),
  { width: 'full', placeholder: '—' },
)

const emit = defineEmits<{
  (e: 'update:modelValue', value: T | null): void
}>()

const open = ref(false)
const root = ref<HTMLDivElement | null>(null)
const listbox = ref<HTMLDivElement | null>(null)
const activeIndex = ref(-1)

const selected = computed(() => props.options.find((o) => o.value === props.modelValue) ?? null)

function pick(opt: SelectOption<T>) {
  if (opt.disabled) return
  emit('update:modelValue', opt.value)
  open.value = false
}

function toggle() {
  if (props.disabled) return
  open.value = !open.value
  if (open.value) {
    const idx = props.options.findIndex((o) => o.value === props.modelValue)
    activeIndex.value = idx >= 0 ? idx : 0
  }
}

function moveActive(delta: number) {
  if (!open.value) {
    open.value = true
    return
  }
  const len = props.options.length
  if (len === 0) return
  let next = activeIndex.value
  for (let i = 0; i < len; i += 1) {
    next = (next + delta + len) % len
    if (!props.options[next].disabled) break
  }
  activeIndex.value = next
}

function onKey(e: KeyboardEvent) {
  if (props.disabled) return
  switch (e.key) {
    case 'ArrowDown':
      e.preventDefault()
      moveActive(1)
      break
    case 'ArrowUp':
      e.preventDefault()
      moveActive(-1)
      break
    case 'Enter':
    case ' ':
      e.preventDefault()
      if (open.value && activeIndex.value >= 0) pick(props.options[activeIndex.value])
      else open.value = true
      break
    case 'Escape':
      open.value = false
      break
    case 'Tab':
      open.value = false
      break
  }
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
      class="flex items-center justify-between gap-2 rounded-md border border-gray-300 bg-white px-3 py-1.5 text-left text-sm hover:border-gray-400 focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 disabled:cursor-not-allowed disabled:bg-gray-50"
      :class="triggerWidthClass"
      :disabled="disabled"
      :aria-expanded="open"
      :aria-haspopup="'listbox'"
      :aria-label="ariaLabel"
      @click="toggle"
      @keydown="onKey"
    >
      <span v-if="selected" class="truncate text-gray-800">{{ selected.label }}</span>
      <span v-else class="truncate text-gray-400">{{ placeholder }}</span>
      <ChevronDown class="h-4 w-4 flex-shrink-0 text-gray-500" />
    </button>

    <div
      v-if="open"
      ref="listbox"
      class="absolute z-20 mt-1 max-h-72 min-w-full overflow-y-auto rounded-md border border-gray-200 bg-white py-1 shadow-lg"
      role="listbox"
    >
      <button
        v-for="(o, i) in options"
        :key="String(o.value)"
        type="button"
        class="flex w-full items-center gap-2 whitespace-nowrap px-3 py-1.5 text-left text-sm hover:bg-blue-50 disabled:cursor-not-allowed disabled:opacity-50"
        :class="{ 'bg-blue-50': o.value === modelValue, 'bg-gray-50': i === activeIndex && o.value !== modelValue }"
        :disabled="o.disabled"
        role="option"
        :aria-selected="o.value === modelValue"
        @click="pick(o)"
        @mouseenter="activeIndex = i"
      >
        <span class="text-gray-800">{{ o.label }}</span>
      </button>
      <p v-if="options.length === 0" class="px-3 py-2 text-xs text-gray-500">—</p>
    </div>
  </div>
</template>
