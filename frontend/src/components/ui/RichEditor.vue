<script setup lang="ts">
import { watch, onBeforeUnmount } from 'vue'
import { useEditor, EditorContent } from '@tiptap/vue-3'
import StarterKit from '@tiptap/starter-kit'
import Link from '@tiptap/extension-link'
import {
  Bold, Italic, List, ListOrdered, Heading2, Heading3,
  Quote, Minus, Link2, Undo2, Redo2,
} from 'lucide-vue-next'

const props = defineProps<{ modelValue: string }>()
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
      class: 'rich-editor-content min-h-[9rem] px-3 py-2 focus:outline-none',
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

function setLink() {
  const prev = editor.value?.getAttributes('link').href as string | undefined
  const url = prompt('URL:', prev ?? 'https://')
  if (url === null) return
  if (url === '') {
    editor.value?.chain().focus().unsetLink().run()
    return
  }
  editor.value?.chain().focus().setLink({ href: url }).run()
}

type Btn =
  | { type: 'button'; icon: unknown; action: () => void; isActive: () => boolean; label: string }
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
    action: setLink,
    isActive: () => editor.value?.isActive('link') ?? false,
  },
]
</script>

<template>
  <div class="overflow-hidden rounded-md border border-gray-300 bg-white focus-within:border-blue-500 focus-within:ring-1 focus-within:ring-blue-500">
    <!-- Toolbar -->
    <div class="flex flex-wrap items-center gap-0.5 border-b border-gray-200 bg-gray-50 px-2 py-1.5">
      <template v-for="(btn, i) in toolbar" :key="i">
        <div v-if="btn.type === 'sep'" class="mx-1 h-4 w-px bg-gray-300" />
        <button
          v-else
          type="button"
          :title="btn.label"
          :class="[
            'rounded p-1 transition',
            btn.isActive()
              ? 'bg-blue-100 text-blue-700'
              : 'text-gray-600 hover:bg-gray-200 hover:text-gray-900',
          ]"
          @click="btn.action()"
        >
          <component :is="btn.icon" class="h-4 w-4" />
        </button>
      </template>
    </div>
    <!-- Editor area -->
    <EditorContent :editor="editor" />
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
