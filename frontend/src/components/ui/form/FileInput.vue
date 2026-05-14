<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { Upload } from 'lucide-vue-next'

const props = withDefaults(
  defineProps<{
    accept?: string
    multiple?: boolean
    disabled?: boolean
    capture?: 'user' | 'environment'
    label?: string
    error?: string
    name?: string
    id?: string
    ariaLabel?: string
    paste?: boolean
    dropzone?: boolean
  }>(),
  { dropzone: true, paste: false },
)

const emit = defineEmits<{
  (e: 'change', files: FileList | null): void
}>()

const fileInput = ref<HTMLInputElement | null>(null)
const selectedName = ref<string>('')
const isDragOver = ref(false)

const acceptMatchers = computed<string[]>(() =>
  (props.accept ?? '')
    .split(',')
    .map((s) => s.trim().toLowerCase())
    .filter(Boolean),
)

function fileMatchesAccept(file: File): boolean {
  if (acceptMatchers.value.length === 0) return true
  const name = file.name.toLowerCase()
  const type = file.type.toLowerCase()
  return acceptMatchers.value.some((m) => {
    if (m.startsWith('.')) return name.endsWith(m)
    if (m.endsWith('/*')) return type.startsWith(m.slice(0, -1))
    return type === m
  })
}

function filterFiles(input: FileList | File[]): File[] {
  const arr = Array.from(input).filter(fileMatchesAccept)
  return props.multiple ? arr : arr.slice(0, 1)
}

function emitFiles(files: FileList | File[] | null) {
  if (!files) {
    selectedName.value = ''
    emit('change', null)
    return
  }
  const filtered = filterFiles(files)
  if (filtered.length === 0) {
    selectedName.value = ''
    emit('change', null)
    return
  }
  selectedName.value =
    filtered.length === 1 ? filtered[0].name : `${filtered.length} files`
  const dt = new DataTransfer()
  for (const f of filtered) dt.items.add(f)
  emit('change', dt.files)
}

function onChange(e: Event) {
  emitFiles((e.target as HTMLInputElement).files)
}

function open() {
  if (props.disabled) return
  fileInput.value?.click()
}

function onDrop(e: DragEvent) {
  isDragOver.value = false
  if (props.disabled) return
  emitFiles(e.dataTransfer?.files ?? null)
}

function onDragOver() {
  if (props.disabled) return
  isDragOver.value = true
}

function onDragLeave() {
  isDragOver.value = false
}

function onPaste(e: ClipboardEvent) {
  if (props.disabled || !props.paste) return
  const items = e.clipboardData?.files
  if (items && items.length > 0) {
    e.preventDefault()
    emitFiles(items)
  }
}

onMounted(() => {
  if (props.paste) window.addEventListener('paste', onPaste)
})
onBeforeUnmount(() => {
  if (props.paste) window.removeEventListener('paste', onPaste)
})
</script>

<template>
  <div>
    <input
      :id="id"
      ref="fileInput"
      :name="name"
      type="file"
      :accept="accept"
      :multiple="multiple"
      :capture="capture"
      :disabled="disabled"
      :aria-label="ariaLabel"
      class="hidden"
      @change="onChange"
    />

    <div
      v-if="dropzone"
      class="flex flex-col items-center justify-center gap-2 rounded-md border-2 border-dashed px-4 py-6 transition-colors"
      :class="[
        isDragOver
          ? 'border-blue-500 bg-blue-50'
          : 'border-gray-300 bg-white hover:border-gray-400',
        disabled && 'cursor-not-allowed opacity-60',
      ]"
      @dragover.prevent="onDragOver"
      @dragenter.prevent="onDragOver"
      @dragleave.prevent="onDragLeave"
      @drop.prevent="onDrop"
    >
      <slot name="trigger" :open="open" :selected-name="selectedName">
        <Upload class="h-5 w-5 text-gray-400" />
        <button
          type="button"
          class="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm text-gray-700 hover:border-gray-400 focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 disabled:cursor-not-allowed disabled:bg-gray-50"
          :disabled="disabled"
          @click="open"
        >
          {{ label ?? 'Choose file' }}
        </button>
        <p class="text-xs text-gray-500">
          <span v-if="selectedName" class="text-gray-700">{{ selectedName }}</span>
          <span v-else>or drag {{ multiple ? 'files' : 'a file' }} here{{ paste ? ' (or paste)' : '' }}</span>
        </p>
      </slot>
    </div>

    <div v-else class="flex items-center gap-2">
      <slot name="trigger" :open="open" :selected-name="selectedName">
        <button
          type="button"
          class="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm text-gray-700 hover:border-gray-400 focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 disabled:cursor-not-allowed disabled:bg-gray-50"
          :disabled="disabled"
          @click="open"
        >
          {{ label ?? 'Choose file' }}
        </button>
        <span class="truncate text-xs text-gray-500">{{ selectedName || 'No file selected' }}</span>
      </slot>
    </div>

    <p v-if="error" class="mt-1 text-xs text-red-600">{{ error }}</p>
  </div>
</template>
