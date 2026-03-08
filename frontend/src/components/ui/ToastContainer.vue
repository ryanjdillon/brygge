<script setup lang="ts">
import { useToast } from '@/composables/useToast'
import { X } from 'lucide-vue-next'

const { toasts, dismiss } = useToast()

const typeClasses: Record<string, string> = {
  success: 'bg-green-50 text-green-800 border-green-200',
  error: 'bg-red-50 text-red-800 border-red-200',
  info: 'bg-blue-50 text-blue-800 border-blue-200',
}
</script>

<template>
  <div aria-live="polite" class="pointer-events-none fixed bottom-4 right-4 z-50 flex flex-col gap-2">
    <div
      v-for="toast in toasts"
      :key="toast.id"
      :class="[
        'pointer-events-auto flex items-center gap-2 rounded-md border px-4 py-3 text-sm shadow-md transition-all',
        typeClasses[toast.type],
      ]"
      role="alert"
    >
      <span class="flex-1">{{ toast.message }}</span>
      <button class="opacity-60 hover:opacity-100" @click="dismiss(toast.id)">
        <X class="h-4 w-4" />
      </button>
    </div>
  </div>
</template>
