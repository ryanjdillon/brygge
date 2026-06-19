<script setup lang="ts">
import { ref, watch, onBeforeUnmount, nextTick } from 'vue'
import { useEditor, EditorContent } from '@tiptap/vue-3'
import StarterKit from '@tiptap/starter-kit'
import Link from '@tiptap/extension-link'
import {
  Bold, Italic, List, ListOrdered, Heading2, Heading3,
  Quote, Minus, Link2, Undo2, Redo2, Paperclip, X as XIcon,
} from 'lucide-vue-next'

const props = defineProps<{
  modelValue: string
  address?: string
}>()
const emit = defineEmits<{ (e: 'update:modelValue', v: string): void }>()

const editor = useEditor({
  content: props.modelValue,
  extensions: [
    StarterKit,
    Link.configure({ openOnClick: false }),
  ],
  onUpdate: ({ editor }) => {
    emit('update:modelValue', editor.getHTML())
  },
  editorProps: {
    attributes: {
      class: 'rich-editor-content px-3 py-2 focus:outline-none',
    },
  },
})

watch(
  () => props.modelValue,
  (val) => {
    if (editor.value && editor.value.getHTML() !== val) {
      editor.value.commands.setContent(val, { emitUpdate: false })
    }
  },
)

onBeforeUnmount(() => editor.value?.destroy())

// ── Link popover ─────────────────────────────────────────────────────────────
const showLinkPopover = ref(false)
const linkUrl = ref('')
const linkInput = ref<HTMLInputElement | null>(null)

function openLinkPopover() {
  linkUrl.value = (editor.value?.getAttributes('link').href as string | undefined) ?? ''
  showLinkPopover.value = true
  nextTick(() => linkInput.value?.focus())
}

function applyLink() {
  const url = linkUrl.value.trim()
  if (!url) {
    editor.value?.chain().focus().unsetLink().run()
  } else {
    editor.value?.chain().focus().setLink({ href: url }).run()
  }
  showLinkPopover.value = false
}

function removeLink() {
  editor.value?.chain().focus().unsetLink().run()
  showLinkPopover.value = false
}

// --- Attachment upload ---------------------------------------------------

interface UploadedFile { blobId: string; name: string; size: number; type: string }
const attachments = ref<UploadedFile[]>([])
const uploading = ref(false)
const fileInput = ref<HTMLInputElement | null>(null)

async function handleFiles(event: Event) {
  const input = event.target as HTMLInputElement
  if (!input.files?.length || !props.address) return
  uploading.value = true
  try {
    for (const file of Array.from(input.files)) {
      const form = new FormData()
      form.append('file', file)
      const res = await fetch(
        `/api/v1/admin/inbox/${encodeURIComponent(props.address)}/blob`,
        { method: 'POST', body: form },
      )
      if (!res.ok) throw new Error(await res.text())
      const data = await res.json()
      attachments.value.push(data)
    }
  } catch (e) {
    console.error('upload failed', e)
  } finally {
    uploading.value = false
    input.value = ''
  }
}

function removeAttachment(blobId: string) {
  attachments.value = attachments.value.filter(a => a.blobId !== blobId)
}

defineExpose({ attachments })

// --- Toolbar ------------------------------------------------------------

type Btn =
  | { type: 'button'; icon: unknown; action: () => void; isActive: () => boolean; label: string; disabled?: () => boolean }
  | { type: 'sep' }

const toolbar: Btn[] = [
  {
    type: 'button', label: 'Angre', icon: Undo2,
    action: () => editor.value?.chain().focus().undo().run(),
    isActive: () => false,
  },
  {
    type: 'button', label: 'Gjer om', icon: Redo2,
    action: () => editor.value?.chain().focus().redo().run(),
    isActive: () => false,
  },
  { type: 'sep' },
  {
    type: 'button', label: 'Fet', icon: Bold,
    action: () => editor.value?.chain().focus().toggleBold().run(),
    isActive: () => editor.value?.isActive('bold') ?? false,
  },
  {
    type: 'button', label: 'Kursiv', icon: Italic,
    action: () => editor.value?.chain().focus().toggleItalic().run(),
    isActive: () => editor.value?.isActive('italic') ?? false,
  },
  { type: 'sep' },
  {
    type: 'button', label: 'Overskrift 2', icon: Heading2,
    action: () => editor.value?.chain().focus().toggleHeading({ level: 2 }).run(),
    isActive: () => editor.value?.isActive('heading', { level: 2 }) ?? false,
  },
  {
    type: 'button', label: 'Overskrift 3', icon: Heading3,
    action: () => editor.value?.chain().focus().toggleHeading({ level: 3 }).run(),
    isActive: () => editor.value?.isActive('heading', { level: 3 }) ?? false,
  },
  { type: 'sep' },
  {
    type: 'button', label: 'Punktliste', icon: List,
    action: () => editor.value?.chain().focus().toggleBulletList().run(),
    isActive: () => editor.value?.isActive('bulletList') ?? false,
  },
  {
    type: 'button', label: 'Nummerert liste', icon: ListOrdered,
    action: () => editor.value?.chain().focus().toggleOrderedList().run(),
    isActive: () => editor.value?.isActive('orderedList') ?? false,
  },
  { type: 'sep' },
  {
    type: 'button', label: 'Sitat', icon: Quote,
    action: () => editor.value?.chain().focus().toggleBlockquote().run(),
    isActive: () => editor.value?.isActive('blockquote') ?? false,
  },
  {
    type: 'button', label: 'Skiljelinje', icon: Minus,
    action: () => editor.value?.chain().focus().setHorizontalRule().run(),
    isActive: () => false,
  },
  {
    type: 'button', label: 'Lenkje', icon: Link2,
    action: openLinkPopover,
    isActive: () => editor.value?.isActive('link') ?? false,
  },
  { type: 'sep' },
  {
    type: 'button', label: 'Vedlegg', icon: Paperclip,
    action: () => { if (props.address) fileInput.value?.click() },
    isActive: () => uploading.value,
    disabled: () => !props.address,
  },
]
</script>

<template>
  <div class="flex flex-col overflow-hidden rounded-md border border-gray-300 bg-white focus-within:border-blue-500 focus-within:ring-1 focus-within:ring-blue-500">
    <!-- Toolbar -->
    <div class="flex flex-wrap items-center gap-0.5 border-b border-gray-200 bg-gray-50 px-2 py-1.5">
      <template v-for="(btn, i) in toolbar" :key="i">
        <div v-if="btn.type === 'sep'" class="mx-1 h-4 w-px bg-gray-300" />
        <button
          v-else
          type="button"
          :title="btn.label"
          :disabled="btn.disabled?.()"
          :class="[
            'rounded p-1 transition',
            btn.isActive()
              ? 'bg-blue-100 text-blue-700'
              : 'text-gray-600 hover:bg-gray-200 hover:text-gray-900',
            btn.disabled?.() ? 'cursor-not-allowed opacity-40' : '',
          ]"
          @click="btn.action()"
        >
          <component :is="btn.icon" class="h-4 w-4" />
        </button>
      </template>
    </div>
    <!-- Link popover -->
    <div
      v-if="showLinkPopover"
      class="flex items-center gap-2 border-b border-blue-100 bg-blue-50 px-3 py-2"
    >
      <Link2 class="h-4 w-4 shrink-0 text-blue-500" />
      <input
        ref="linkInput"
        v-model="linkUrl"
        type="url"
        placeholder="https://"
        class="min-w-0 flex-1 rounded border border-gray-300 px-2 py-1 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        @keydown.enter.prevent="applyLink"
        @keydown.esc.prevent="showLinkPopover = false"
      />
      <button
        type="button"
        class="rounded bg-blue-600 px-2.5 py-1 text-xs font-medium text-white hover:bg-blue-700"
        @click="applyLink"
      >
        Bruk
      </button>
      <button
        v-if="editor?.isActive('link')"
        type="button"
        class="rounded border border-gray-300 px-2.5 py-1 text-xs font-medium text-gray-600 hover:bg-gray-100"
        @click="removeLink"
      >
        Fjern
      </button>
      <button
        type="button"
        class="rounded p-0.5 text-gray-400 hover:text-gray-700"
        @click="showLinkPopover = false"
      >
        <XIcon class="h-4 w-4" />
      </button>
    </div>
    <!-- Editor area -->
    <div class="min-h-[8rem] flex-1 overflow-y-auto">
      <EditorContent :editor="editor" />
    </div>
    <!-- Hidden file input -->
    <input
      v-if="address"
      ref="fileInput"
      type="file"
      multiple
      class="sr-only"
      @change="handleFiles"
    />
    <!-- Attachment chips -->
    <div v-if="attachments.length || uploading" class="flex flex-wrap gap-1.5 border-t border-gray-200 px-3 py-2">
      <span
        v-for="a in attachments"
        :key="a.blobId"
        class="inline-flex items-center gap-1 rounded-full bg-gray-100 pl-2.5 pr-1.5 py-0.5 text-xs text-gray-700"
      >
        <Paperclip class="h-3 w-3 text-gray-400" />
        {{ a.name }}
        <button type="button" class="rounded-full p-0.5 hover:bg-gray-300" @click="removeAttachment(a.blobId)">
          <XIcon class="h-3 w-3" />
        </button>
      </span>
      <span v-if="uploading" class="text-xs text-gray-400 italic">Lastar opp…</span>
    </div>
  </div>
</template>

<style scoped>
:deep(.rich-editor-content) {
  h2 { font-size: 1.25rem; font-weight: 600; margin: 0.75rem 0 0.25rem; }
  h3 { font-size: 1.1rem; font-weight: 600; margin: 0.5rem 0 0.25rem; }
  p { margin: 0.25rem 0; }
  ul { list-style-type: disc; padding-left: 1.5rem; margin: 0.25rem 0; }
  ol { list-style-type: decimal; padding-left: 1.5rem; margin: 0.25rem 0; }
  blockquote {
    border-left: 3px solid #d1d5db;
    padding-left: 0.75rem;
    color: #6b7280;
    margin: 0.5rem 0;
  }
  hr { border-color: #e5e7eb; margin: 0.75rem 0; }
  a { color: #2563eb; text-decoration: underline; }
  strong { font-weight: 600; }
  em { font-style: italic; }
}
:deep(.rich-editor-content p.is-editor-empty:first-child::before) {
  content: attr(data-placeholder);
  color: #9ca3af;
  pointer-events: none;
  float: left;
  height: 0;
}
</style>
