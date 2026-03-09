<script setup lang="ts">
import { ref, computed, watch, onMounted, onBeforeUnmount } from 'vue'
import { ChevronDown } from 'lucide-vue-next'

const model = defineModel<string>({ required: true })
const props = defineProps<{ hasError?: boolean }>()
const emit = defineEmits<{ blur: [] }>()

const countryCodes = [
  { code: '+47', country: 'NO' },
  { code: '+46', country: 'SE' },
  { code: '+45', country: 'DK' },
  { code: '+358', country: 'FI' },
  { code: '+49', country: 'DE' },
  { code: '+44', country: 'GB' },
  { code: '+31', country: 'NL' },
  { code: '+33', country: 'FR' },
  { code: '+34', country: 'ES' },
  { code: '+39', country: 'IT' },
  { code: '+48', country: 'PL' },
  { code: '+1', country: 'US' },
]

const selectedCode = ref('+47')
const localNumber = ref('')
const open = ref(false)

watch(model, (val) => {
  if (!val) return
  const match = countryCodes.find((c) => val.startsWith(c.code))
  if (match) {
    selectedCode.value = match.code
    localNumber.value = val.slice(match.code.length).trim()
  } else if (!localNumber.value) {
    localNumber.value = val
  }
}, { immediate: true })

const fullNumber = computed(() => {
  const num = localNumber.value.replace(/\s/g, '')
  if (!num) return ''
  return `${selectedCode.value}${num}`
})

watch(fullNumber, (val) => {
  model.value = val
})

function selectCountry(code: string) {
  selectedCode.value = code
  open.value = false
}

const selectedCountry = computed(() => countryCodes.find((c) => c.code === selectedCode.value)?.country ?? '')

const wrapper = ref<HTMLElement>()

function onClickOutside(e: MouseEvent) {
  if (wrapper.value && !wrapper.value.contains(e.target as Node)) {
    open.value = false
  }
}

onMounted(() => document.addEventListener('click', onClickOutside))
onBeforeUnmount(() => document.removeEventListener('click', onClickOutside))

const borderClass = computed(() => props.hasError ? 'border-red-400' : 'border-gray-300')
</script>

<template>
  <div ref="wrapper" class="relative flex items-stretch">
    <button
      type="button"
      class="flex shrink-0 items-center gap-1.5 rounded-l-md border bg-gray-50 px-3 text-sm transition hover:bg-gray-100"
      :class="[borderClass, 'border-r-0']"
      @click="open = !open"
    >
      <span class="inline-flex h-5 w-7 items-center justify-center rounded-sm bg-gray-200 text-[10px] font-bold leading-none text-gray-600">{{ selectedCountry }}</span>
      <span class="text-gray-600">{{ selectedCode }}</span>
      <ChevronDown class="h-3 w-3 text-gray-400 transition" :class="open ? 'rotate-180' : ''" />
    </button>

    <ul
      v-if="open"
      class="absolute left-0 top-[calc(100%+4px)] z-30 max-h-60 w-52 overflow-auto rounded-md border border-gray-200 bg-white py-1 shadow-lg ring-1 ring-black/5"
    >
      <li
        v-for="cc in countryCodes"
        :key="cc.code"
        class="flex cursor-pointer items-center gap-2.5 px-3 py-2 text-sm transition-colors hover:bg-gray-50"
        :class="cc.code === selectedCode ? 'bg-blue-50 text-blue-700 font-medium' : 'text-gray-700'"
        @click="selectCountry(cc.code)"
      >
        <span class="inline-flex h-5 w-7 items-center justify-center rounded-sm bg-gray-200 text-[10px] font-bold leading-none text-gray-500">{{ cc.country }}</span>
        <span>{{ cc.country }}</span>
        <span class="ml-auto tabular-nums text-gray-400">{{ cc.code }}</span>
      </li>
    </ul>

    <input
      v-model="localNumber"
      type="tel"
      inputmode="tel"
      class="block w-full min-w-0 rounded-r-md border text-sm focus:border-blue-500 focus:ring-blue-500"
      :class="borderClass"
      placeholder="912 34 567"
      @blur="emit('blur')"
    />
  </div>
</template>
