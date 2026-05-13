<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { ChevronDown } from 'lucide-vue-next'
import AccountCodeChip from '@/components/ui/AccountCodeChip.vue'
import type { Account } from '@/composables/useAccounting'

const props = defineProps<{
  modelValue: string
  options: Account[]
  placeholder?: string
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
}>()

const open = ref(false)
const root = ref<HTMLDivElement | null>(null)

const selected = computed(() => props.options.find((o) => o.code === props.modelValue))

function pick(code: string) {
  emit('update:modelValue', code)
  open.value = false
}

function onDocClick(e: MouseEvent) {
  if (!root.value) return
  if (!root.value.contains(e.target as Node)) open.value = false
}

onMounted(() => document.addEventListener('mousedown', onDocClick))
onBeforeUnmount(() => document.removeEventListener('mousedown', onDocClick))
</script>

<template>
  <div ref="root" class="relative">
    <button
      type="button"
      class="flex w-full items-center justify-between gap-2 rounded-md border border-gray-300 bg-white px-3 py-1.5 text-left text-sm hover:border-gray-400"
      :aria-expanded="open"
      @click="open = !open"
    >
      <span v-if="selected" class="flex items-center gap-2">
        <AccountCodeChip :code="selected.code" :account-type="selected.account_type" :is-system="selected.is_system" size="sm" />
        <span class="text-gray-800">{{ selected.name }}</span>
      </span>
      <span v-else class="text-gray-400">{{ placeholder ?? '—' }}</span>
      <ChevronDown class="h-4 w-4 text-gray-500" />
    </button>

    <div
      v-if="open"
      class="absolute z-20 mt-1 max-h-72 w-full overflow-y-auto rounded-md border border-gray-200 bg-white py-1 shadow-lg"
      role="listbox"
    >
      <button
        v-for="o in options"
        :key="o.id"
        type="button"
        class="flex w-full items-center gap-2 px-2 py-1.5 text-left text-sm hover:bg-blue-50"
        :class="{ 'bg-blue-50': o.code === modelValue }"
        role="option"
        :aria-selected="o.code === modelValue"
        @click="pick(o.code)"
      >
        <AccountCodeChip :code="o.code" :account-type="o.account_type" :is-system="o.is_system" size="sm" />
        <span class="text-gray-800">{{ o.name }}</span>
      </button>
      <p v-if="options.length === 0" class="px-2 py-2 text-xs text-gray-500">—</p>
    </div>
  </div>
</template>
