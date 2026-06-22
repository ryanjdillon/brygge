<script setup lang="ts">
import { ref, watch, onBeforeUnmount, nextTick } from 'vue'
import { useEditor, EditorContent } from '@tiptap/vue-3'
import StarterKit from '@tiptap/starter-kit'
import Link from '@tiptap/extension-link'
import Image from '@tiptap/extension-image'
import { useFreshTotp } from '@/composables/useFreshTotp'
import {
  Bold, Italic, Strikethrough, List, ListOrdered, Heading2, Heading3,
  Quote, Minus, Link2, Undo2, Redo2, Paperclip, Image as ImageIcon,
  X as XIcon, RemoveFormatting,
} from 'lucide-vue-next'

const props = defineProps<{
  modelValue: string
  address?: string
}>()
const emit = defineEmits<{ (e: 'update:modelValue', v: string): void }>()

// ── Upload helpers ────────────────────────────────────────────────────────────

interface UploadedFile { blobId: string; name: string; size: number; type: string }
interface PendingFile { id: string; name: string; status: 'uploading' | 'error'; error?: string }
interface InlineImage { cid: string; blobId: string; name: string; type: string; src: string }

const attachments = ref<UploadedFile[]>([])
const pending = ref<PendingFile[]>([])
const inlineImages = ref<InlineImage[]>([])
const fileInput = ref<HTMLInputElement | null>(null)
const imageInput = ref<HTMLInputElement | null>(null)

let nextId = 0

const { totpAwareFetch } = useFreshTotp()

async function uploadBlob(file: File): Promise<UploadedFile> {
  const form = new FormData()
  form.append('file', file)
  const res = await totpAwareFetch(
    `/api/v1/admin/inbox/${encodeURIComponent(props.address!)}/blob`,
    { method: 'POST', body: form },
  )
  if (!res.ok) {
    let msg = `Feil ${res.status}`
    try { const j = await res.json(); msg = j?.error ?? j?.message ?? msg } catch { /* ignore */ }
    throw new Error(msg)
  }
  return res.json()
}

function blobSrc(blobId: string, name: string) {
  return `/api/v1/admin/inbox/${encodeURIComponent(props.address!)}/blob/${encodeURIComponent(blobId)}?name=${encodeURIComponent(name)}`
}

async function uploadAttachments(files: File[]) {
  await Promise.all(files.map(async (file) => {
    const id = String(nextId++)
    pending.value.push({ id, name: file.name, status: 'uploading' })
    try {
      const data = await uploadBlob(file)
      attachments.value.push(data)
      pending.value = pending.value.filter(p => p.id !== id)
    } catch (e) {
      const idx = pending.value.findIndex(p => p.id === id)
      if (idx >= 0) pending.value[idx] = { id, name: file.name, status: 'error', error: (e as Error).message }
    }
  }))
}

async function uploadAndInsertImage(file: File) {
  if (!props.address) return
  const id = String(nextId++)
  pending.value.push({ id, name: file.name, status: 'uploading' })
  try {
    const data = await uploadBlob(file)
    const src = blobSrc(data.blobId, data.name || file.name)
    const cid = `img-${data.blobId.replace(/[^a-zA-Z0-9]/g, '').slice(0, 32)}@klokkarvikbaatlag.no`
    inlineImages.value.push({ cid, blobId: data.blobId, name: data.name || file.name, type: data.type, src })
    editor.value?.chain().focus().setImage({ src, alt: data.name || file.name }).run()
    pending.value = pending.value.filter(p => p.id !== id)
  } catch (e) {
    const idx = pending.value.findIndex(p => p.id === id)
    if (idx >= 0) pending.value[idx] = { id, name: file.name, status: 'error', error: (e as Error).message }
  }
}

function removeAttachment(blobId: string) {
  attachments.value = attachments.value.filter(a => a.blobId !== blobId)
}

function dismissPending(id: string) {
  pending.value = pending.value.filter(p => p.id !== id)
}

// ── Editor ───────────────────────────────────────────────────────────────────

const editor = useEditor({
  content: props.modelValue,
  extensions: [
    StarterKit,
    Link.configure({ openOnClick: false }),
    Image.configure({ allowBase64: false }),
  ],
  onUpdate: ({ editor }) => {
    emit('update:modelValue', editor.getHTML())
  },
  editorProps: {
    attributes: {
      class: 'rich-editor-content px-3 py-2 focus:outline-none',
    },
    handlePaste(_, event) {
      if (!props.address) return false
      const items = Array.from(event.clipboardData?.items ?? [])
      const images = items.filter(i => i.kind === 'file' && i.type.startsWith('image/'))
      if (!images.length) return false
      images.forEach(item => {
        const file = item.getAsFile()
        if (file) uploadAndInsertImage(file)
      })
      return true
    },
    handleDrop(_, event) {
      if (!props.address) return false
      const files = Array.from((event as DragEvent).dataTransfer?.files ?? [])
        .filter(f => f.type.startsWith('image/'))
      if (!files.length) return false
      files.forEach(f => uploadAndInsertImage(f))
      return true
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

// ── File input handlers ───────────────────────────────────────────────────────

async function handleFiles(event: Event) {
  const input = event.target as HTMLInputElement
  if (!input.files?.length || !props.address) return
  const files = Array.from(input.files)
  input.value = ''
  await uploadAttachments(files)
}

async function handleImageFiles(event: Event) {
  const input = event.target as HTMLInputElement
  if (!input.files?.length || !props.address) return
  const files = Array.from(input.files)
  input.value = ''
  await Promise.all(files.map(uploadAndInsertImage))
}

defineExpose({ attachments, pending, inlineImages })

// ── Toolbar ───────────────────────────────────────────────────────────────────

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
  {
    type: 'button', label: 'Gjennomstrek', icon: Strikethrough,
    action: () => editor.value?.chain().focus().toggleStrike().run(),
    isActive: () => editor.value?.isActive('strike') ?? false,
  },
  {
    type: 'button', label: 'Fjern formatering', icon: RemoveFormatting,
    action: () => editor.value?.chain().focus().unsetAllMarks().clearNodes().run(),
    isActive: () => false,
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
    type: 'button', label: 'Set inn bilete', icon: ImageIcon,
    action: () => { if (props.address) imageInput.value?.click() },
    isActive: () => false,
    disabled: () => !props.address,
  },
  {
    type: 'button', label: 'Vedlegg', icon: Paperclip,
    action: () => { if (props.address) fileInput.value?.click() },
    isActive: () => pending.value.some(p => p.status === 'uploading'),
    disabled: () => !props.address,
  },
]
</script>

<template>
  <div class="flex flex-col overflow-hidden rounded-md border border-gray-300 bg-white focus-within:border-brand-500 focus-within:ring-1 focus-within:ring-brand-500">
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
              ? 'bg-brand-100 text-brand-700'
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
      class="flex items-center gap-2 border-b border-brand-100 bg-brand-50 px-3 py-2"
    >
      <Link2 class="h-4 w-4 shrink-0 text-brand-500" />
      <input
        ref="linkInput"
        v-model="linkUrl"
        type="url"
        placeholder="https://"
        class="min-w-0 flex-1 rounded border border-gray-300 px-2 py-1 text-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
        @keydown.enter.prevent="applyLink"
        @keydown.esc.prevent="showLinkPopover = false"
      />
      <button
        type="button"
        class="rounded bg-brand-600 px-2.5 py-1 text-xs font-medium text-white hover:bg-brand-700"
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
    <!-- Hidden file inputs -->
    <input
      v-if="address"
      ref="imageInput"
      type="file"
      accept="image/*"
      multiple
      class="sr-only"
      @change="handleImageFiles"
    />
    <input
      v-if="address"
      ref="fileInput"
      type="file"
      multiple
      class="sr-only"
      @change="handleFiles"
    />
    <!-- Attachment chips -->
    <div v-if="attachments.length || pending.length" class="flex flex-wrap gap-1.5 border-t border-gray-200 px-3 py-2">
      <!-- Uploaded -->
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
      <!-- Pending: uploading or error -->
      <span
        v-for="p in pending"
        :key="p.id"
        :class="[
          'inline-flex items-center gap-1.5 rounded-full pl-2.5 pr-1.5 py-0.5 text-xs',
          p.status === 'error'
            ? 'bg-red-50 text-red-700 ring-1 ring-red-200'
            : 'bg-brand-50 text-brand-700',
        ]"
        :title="p.status === 'error' ? p.error : undefined"
      >
        <svg
          v-if="p.status === 'uploading'"
          class="h-3 w-3 animate-spin text-brand-500"
          viewBox="0 0 24 24"
          fill="none"
        >
          <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
          <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v4a4 4 0 00-4 4H4z" />
        </svg>
        <span class="max-w-[160px] truncate">{{ p.name }}</span>
        <span v-if="p.status === 'error'" class="truncate text-red-500">— {{ p.error }}</span>
        <button
          v-if="p.status === 'error'"
          type="button"
          class="rounded-full p-0.5 hover:bg-red-100"
          @click="dismissPending(p.id)"
        >
          <XIcon class="h-3 w-3" />
        </button>
      </span>
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
  s, del { text-decoration: line-through; }
  img {
    max-width: 100%;
    height: auto;
    border-radius: 0.25rem;
    display: block;
    margin: 0.5rem 0;
    cursor: default;
  }
  img.ProseMirror-selectednode {
    outline: 2px solid #3b82f6;
    outline-offset: 2px;
  }
}
:deep(.rich-editor-content p.is-editor-empty:first-child::before) {
  content: attr(data-placeholder);
  color: #9ca3af;
  pointer-events: none;
  float: left;
  height: 0;
}
</style>
