<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { ChevronDown } from 'lucide-vue-next'

const { locale } = useI18n()
const open = ref(false)
const dropdownRef = ref<HTMLElement>()

const languages = [
  { code: 'nb', label: 'Norsk' },
  { code: 'en', label: 'English' },
  { code: 'de', label: 'Deutsch' },
  { code: 'fr', label: 'Français' },
  { code: 'nl', label: 'Nederlands' },
  { code: 'it', label: 'Italiano' },
  { code: 'pl', label: 'Polski' },
]

const currentLabel = () => languages.find((l) => l.code === locale.value)?.label ?? locale.value

function select(code: string) {
  locale.value = code
  localStorage.setItem('brygge-locale', code)
  open.value = false
}

function handleClickOutside(event: MouseEvent) {
  if (dropdownRef.value && !dropdownRef.value.contains(event.target as Node)) {
    open.value = false
  }
}

onMounted(() => document.addEventListener('click', handleClickOutside))
onUnmounted(() => document.removeEventListener('click', handleClickOutside))
</script>

<template>
  <div ref="dropdownRef" class="relative">
    <button
      type="button"
      class="flex items-center gap-1 rounded-md px-2 py-1.5 text-sm font-medium text-gray-600 hover:bg-gray-100 hover:text-gray-900"
      @click.stop="open = !open"
    >
      {{ currentLabel() }}
      <ChevronDown
        class="h-3 w-3 transition-transform"
        :class="{ 'rotate-180': open }"
        aria-hidden="true"
      />
    </button>
    <div
      v-if="open"
      class="absolute right-0 top-full z-50 mt-1 w-36 rounded-md bg-white py-1 shadow-lg ring-1 ring-black/5"
    >
      <button
        v-for="lang in languages"
        :key="lang.code"
        type="button"
        class="block w-full px-4 py-2 text-left text-sm hover:bg-gray-100"
        :class="lang.code === locale ? 'font-semibold text-blue-600' : 'text-gray-700'"
        @click="select(lang.code)"
      >
        {{ lang.label }}
      </button>
    </div>
  </div>
</template>
