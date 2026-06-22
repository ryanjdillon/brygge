<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { ChevronDown } from 'lucide-vue-next'

// Account picker that renders the account code as a colored chip in
// each option (and on the selected display) so the form mirrors the
// chart-of-accounts page palette. Native <select> can't render
// per-option HTML so this is a button-triggered listbox instead.

interface Account {
  id: string
  code: string
  name: string
  account_type: 'asset' | 'liability' | 'revenue' | 'expense' | string
  is_active: boolean
}

const props = defineProps<{
  modelValue: string
  accounts: Account[]
  placeholder?: string
}>()
const emit = defineEmits<{ (e: 'update:modelValue', v: string): void }>()

// Same palette the Chart of Accounts page uses.
function accountChipClass(type: string): string {
  switch (type) {
    case 'asset': return 'bg-indigo-100 text-indigo-800'
    case 'liability': return 'bg-amber-100 text-amber-800'
    case 'revenue': return 'bg-green-100 text-green-800'
    case 'expense': return 'bg-red-100 text-red-800'
    default: return 'bg-slate-100 text-slate-700'
  }
}

const open = ref(false)
const root = ref<HTMLElement>()

const selected = computed(() => props.accounts.find((a) => a.id === props.modelValue))

function pick(id: string) {
  emit('update:modelValue', id)
  open.value = false
}

function onClickOutside(e: MouseEvent) {
  if (root.value && !root.value.contains(e.target as Node)) open.value = false
}
onMounted(() => document.addEventListener('click', onClickOutside))
onUnmounted(() => document.removeEventListener('click', onClickOutside))
</script>

<template>
  <div ref="root" class="relative">
    <button
      type="button"
      class="flex w-full items-center justify-between gap-2 rounded-md border border-slate-300 bg-white px-2 py-1 text-left text-xs hover:border-slate-400"
      @click.stop="open = !open"
    >
      <span v-if="selected" class="flex min-w-0 items-center gap-2">
        <span :class="['shrink-0 rounded-full px-2 py-0.5 text-[10px] font-semibold', accountChipClass(selected.account_type)]">
          {{ selected.code }}
        </span>
        <span class="truncate text-slate-700">{{ selected.name }}</span>
      </span>
      <span v-else class="text-slate-400">{{ placeholder || '—' }}</span>
      <ChevronDown class="h-3.5 w-3.5 shrink-0 text-slate-400" :class="{ 'rotate-180': open }" />
    </button>

    <div
      v-if="open"
      class="absolute left-0 right-0 top-full z-30 mt-1 max-h-60 overflow-y-auto rounded-md border border-slate-200 bg-white py-1 shadow-lg"
    >
      <button
        v-for="a in accounts"
        :key="a.id"
        type="button"
        class="flex w-full items-center gap-2 px-2 py-1.5 text-left text-xs hover:bg-slate-50"
        :class="{ 'bg-slate-50': a.id === modelValue }"
        @click="pick(a.id)"
      >
        <span :class="['shrink-0 rounded-full px-2 py-0.5 text-[10px] font-semibold', accountChipClass(a.account_type)]">
          {{ a.code }}
        </span>
        <span class="truncate text-slate-700">{{ a.name }}</span>
      </button>
      <div v-if="!accounts.length" class="px-3 py-3 text-center text-xs text-slate-400">—</div>
    </div>
  </div>
</template>
