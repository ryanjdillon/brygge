<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApiClient, unwrap } from '@/lib/apiClient'
import { formatDate } from '@/lib/format'
import { Upload, FilePen, Trash2, X, FileText, FileType } from 'lucide-vue-next'
import RichEditor from '@/components/ui/RichEditor.vue'

const { t } = useI18n()
const client = useApiClient()
const queryClient = useQueryClient()

// ── File documents ────────────────────────────────────────────────────────────

const { data: fileResponse, isLoading: fileLoading, isError: fileError } = useQuery({
  queryKey: ['admin', 'documents'],
  queryFn: async () => unwrap(await client.GET('/api/v1/documents')),
})
const fileDocs = computed(() => fileResponse.value?.documents ?? [])

// ── Content documents ─────────────────────────────────────────────────────────

const { data: contentResponse, isLoading: contentLoading, isError: contentError } = useQuery({
  queryKey: ['admin', 'content-documents'],
  queryFn: async () => {
    const res = await fetch('/api/v1/admin/content-documents', { credentials: 'include' })
    if (!res.ok) throw new Error('failed')
    return res.json()
  },
})
const contentDocs = computed(() => contentResponse.value?.documents ?? [])

const isLoading = computed(() => fileLoading.value || contentLoading.value)
const isError = computed(() => fileError.value || contentError.value)

// ── Toast ─────────────────────────────────────────────────────────────────────

const toast = ref<{ type: 'success' | 'error'; message: string } | null>(null)

function showToast(type: 'success' | 'error', message: string) {
  toast.value = { type, message }
  setTimeout(() => (toast.value = null), 3000)
}

// ── Visibility helpers ────────────────────────────────────────────────────────

const visibilityOptions = [
  { value: 'member', labelKey: 'admin.documents.visibilityMember' },
  { value: 'board', labelKey: 'admin.documents.visibilityBoard' },
  { value: 'slip_holder', labelKey: 'admin.documents.visibilitySlipHolder' },
]

function visibilityLabel(v: string) {
  if (v === 'board') return t('admin.documents.visibilityBoard')
  if (v === 'slip_holder') return t('admin.documents.visibilitySlipHolder')
  return t('admin.documents.visibilityMember')
}

function visibilityClass(v: string) {
  if (v === 'board') return 'bg-purple-100 text-purple-700'
  if (v === 'slip_holder') return 'bg-blue-100 text-blue-700'
  return 'bg-gray-100 text-gray-700'
}

// ── Upload modal ──────────────────────────────────────────────────────────────

const showUploadModal = ref(false)
const uploadTitle = ref('')
const uploadFile = ref<File | null>(null)
const uploadVisibility = ref('member')
const uploadError = ref('')

function openUploadModal() {
  uploadTitle.value = ''
  uploadFile.value = null
  uploadVisibility.value = 'member'
  uploadError.value = ''
  showUploadModal.value = true
}

function handleFileChange(e: Event) {
  const input = e.target as HTMLInputElement
  uploadFile.value = input.files?.[0] ?? null
  if (!uploadTitle.value && uploadFile.value) {
    uploadTitle.value = uploadFile.value.name.replace(/\.[^.]+$/, '')
  }
}

const { mutate: uploadDocument, isPending: uploading } = useMutation({
  mutationFn: async () => {
    if (!uploadFile.value) throw new Error('no file')
    const form = new FormData()
    form.append('file', uploadFile.value)
    form.append('title', uploadTitle.value || uploadFile.value.name)
    form.append('visibility', uploadVisibility.value)
    const res = await fetch('/api/v1/admin/documents', {
      method: 'POST',
      credentials: 'include',
      body: form,
    })
    if (!res.ok) {
      const j = await res.json().catch(() => ({}))
      throw new Error(j?.error ?? `HTTP ${res.status}`)
    }
    return res.json()
  },
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['admin', 'documents'] })
    showUploadModal.value = false
    showToast('success', t('admin.documents.uploadSuccess'))
  },
  onError: (e: Error) => {
    uploadError.value = e.message
  },
})

// ── Delete file document ──────────────────────────────────────────────────────

const { mutate: deleteFileDoc } = useMutation({
  mutationFn: async (docID: string) => {
    const res = await fetch(`/api/v1/admin/documents/${docID}`, {
      method: 'DELETE',
      credentials: 'include',
    })
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
  },
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['admin', 'documents'] })
    showToast('success', t('admin.documents.deleteSuccess'))
  },
  onError: () => showToast('error', t('admin.documents.deleteError')),
})

function confirmDeleteFile(docID: string) {
  if (confirm(t('admin.documents.deleteConfirm'))) deleteFileDoc(docID)
}

// ── Authored doc modal ────────────────────────────────────────────────────────

const showAuthoredModal = ref(false)
const editingDocID = ref<string | null>(null)
const authoredTitle = ref('')
const authoredBody = ref('')
const authoredVisibility = ref('member')
const authoredPublished = ref(false)
const authoredError = ref('')

function openCreateAuthoredModal() {
  editingDocID.value = null
  authoredTitle.value = ''
  authoredBody.value = ''
  authoredVisibility.value = 'member'
  authoredPublished.value = false
  authoredError.value = ''
  showAuthoredModal.value = true
}

function openEditAuthoredModal(doc: { id: string; title: string; body_html: string; visibility: string; published: boolean }) {
  editingDocID.value = doc.id
  authoredTitle.value = doc.title
  authoredBody.value = doc.body_html
  authoredVisibility.value = doc.visibility
  authoredPublished.value = doc.published
  authoredError.value = ''
  showAuthoredModal.value = true
}

const { mutate: saveAuthoredDoc, isPending: savingAuthored } = useMutation({
  mutationFn: async () => {
    const body = {
      title: authoredTitle.value.trim(),
      body_html: authoredBody.value,
      visibility: authoredVisibility.value,
      published: authoredPublished.value,
    }
    const isEdit = editingDocID.value !== null
    const url = isEdit
      ? `/api/v1/admin/content-documents/${editingDocID.value}`
      : '/api/v1/admin/content-documents'
    const res = await fetch(url, {
      method: isEdit ? 'PUT' : 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })
    if (!res.ok) {
      const j = await res.json().catch(() => ({}))
      throw new Error(j?.error ?? `HTTP ${res.status}`)
    }
  },
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['admin', 'content-documents'] })
    showAuthoredModal.value = false
    showToast('success', t('admin.documents.saveSuccess'))
  },
  onError: (e: Error) => {
    authoredError.value = e.message
  },
})

// ── Delete content document ───────────────────────────────────────────────────

const { mutate: deleteContentDoc } = useMutation({
  mutationFn: async (docID: string) => {
    const res = await fetch(`/api/v1/admin/content-documents/${docID}`, {
      method: 'DELETE',
      credentials: 'include',
    })
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
  },
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['admin', 'content-documents'] })
    showToast('success', t('admin.documents.deleteSuccess'))
  },
  onError: () => showToast('error', t('admin.documents.deleteError')),
})

function confirmDeleteContent(docID: string) {
  if (confirm(t('admin.documents.deleteConfirm'))) deleteContentDoc(docID)
}
</script>

<template>
  <div>
    <div class="flex items-center justify-between">
      <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.sidebar.documents') }}</h1>
      <div class="flex gap-2">
        <button
          type="button"
          class="inline-flex items-center gap-1.5 rounded-md bg-white px-3 py-2 text-sm font-medium text-gray-700 shadow-sm ring-1 ring-inset ring-gray-300 hover:bg-gray-50"
          @click="openUploadModal"
        >
          <Upload class="h-4 w-4" />
          {{ t('admin.documents.uploadFile') }}
        </button>
        <button
          type="button"
          class="inline-flex items-center gap-1.5 rounded-md bg-blue-600 px-3 py-2 text-sm font-medium text-white shadow-sm hover:bg-blue-700"
          @click="openCreateAuthoredModal"
        >
          <FilePen class="h-4 w-4" />
          {{ t('admin.documents.createDocument') }}
        </button>
      </div>
    </div>

    <!-- Toast -->
    <div
      v-if="toast"
      :class="[
        'mt-4 rounded-md p-3 text-sm',
        toast.type === 'success' ? 'bg-green-50 text-green-800' : 'bg-red-50 text-red-800',
      ]"
    >
      {{ toast.message }}
    </div>

    <div v-if="isLoading" class="mt-6 text-gray-500">{{ t('common.loading') }}...</div>
    <div v-else-if="isError" class="mt-6 rounded-md bg-red-50 p-3 text-sm text-red-800">{{ t('admin.documents.loadError') }}</div>

    <template v-else>
      <!-- File uploads -->
      <section class="mt-6">
        <h2 class="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-400">
          {{ t('admin.documents.sectionFiles') }}
        </h2>
        <div v-if="!fileDocs.length" class="text-sm text-gray-500">{{ t('admin.documents.noDocuments') }}</div>
        <div v-else class="overflow-x-auto">
          <table class="min-w-full divide-y divide-gray-200">
            <thead class="bg-gray-50">
              <tr>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.documents.title') }}</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.documents.file') }}</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.documents.visibility') }}</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.documents.date') }}</th>
                <th class="px-4 py-3" />
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-200 bg-white">
              <tr v-for="doc in fileDocs" :key="doc.id">
                <td class="whitespace-nowrap px-4 py-3 text-sm font-medium text-gray-900">
                  <span class="inline-flex items-center gap-1.5">
                    <FileType class="h-4 w-4 text-gray-400" />
                    {{ doc.title }}
                  </span>
                </td>
                <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ doc.filename }}</td>
                <td class="whitespace-nowrap px-4 py-3 text-sm">
                  <span :class="['rounded-full px-2.5 py-0.5 text-xs font-medium', visibilityClass(doc.visibility)]">
                    {{ visibilityLabel(doc.visibility) }}
                  </span>
                </td>
                <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ formatDate(doc.created_at) }}</td>
                <td class="whitespace-nowrap px-4 py-3 text-right">
                  <button
                    type="button"
                    class="rounded p-1 text-gray-400 hover:bg-red-50 hover:text-red-600"
                    :title="t('common.delete')"
                    @click="confirmDeleteFile(doc.id)"
                  >
                    <Trash2 class="h-4 w-4" />
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <!-- Authored documents -->
      <section class="mt-8">
        <h2 class="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-400">
          {{ t('admin.documents.sectionAuthored') }}
        </h2>
        <div v-if="!contentDocs.length" class="text-sm text-gray-500">{{ t('admin.documents.noAuthoredDocuments') }}</div>
        <div v-else class="overflow-x-auto">
          <table class="min-w-full divide-y divide-gray-200">
            <thead class="bg-gray-50">
              <tr>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.documents.title') }}</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.documents.visibility') }}</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.documents.status') }}</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">{{ t('admin.documents.date') }}</th>
                <th class="px-4 py-3" />
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-200 bg-white">
              <tr v-for="doc in contentDocs" :key="doc.id">
                <td class="whitespace-nowrap px-4 py-3 text-sm font-medium text-gray-900">
                  <span class="inline-flex items-center gap-1.5">
                    <FileText class="h-4 w-4 text-gray-400" />
                    {{ doc.title }}
                  </span>
                </td>
                <td class="whitespace-nowrap px-4 py-3 text-sm">
                  <span :class="['rounded-full px-2.5 py-0.5 text-xs font-medium', visibilityClass(doc.visibility)]">
                    {{ visibilityLabel(doc.visibility) }}
                  </span>
                </td>
                <td class="whitespace-nowrap px-4 py-3 text-sm">
                  <span
                    :class="[
                      'rounded-full px-2.5 py-0.5 text-xs font-medium',
                      doc.published ? 'bg-green-100 text-green-700' : 'bg-yellow-100 text-yellow-700',
                    ]"
                  >
                    {{ doc.published ? t('admin.documents.published') : t('admin.documents.draft') }}
                  </span>
                </td>
                <td class="whitespace-nowrap px-4 py-3 text-sm text-gray-500">{{ formatDate(doc.created_at) }}</td>
                <td class="whitespace-nowrap px-4 py-3 text-right">
                  <div class="flex justify-end gap-1">
                    <button
                      type="button"
                      class="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-700"
                      :title="t('common.edit')"
                      @click="openEditAuthoredModal(doc)"
                    >
                      <FilePen class="h-4 w-4" />
                    </button>
                    <button
                      type="button"
                      class="rounded p-1 text-gray-400 hover:bg-red-50 hover:text-red-600"
                      :title="t('common.delete')"
                      @click="confirmDeleteContent(doc.id)"
                    >
                      <Trash2 class="h-4 w-4" />
                    </button>
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </template>

    <!-- Upload file modal -->
    <div v-if="showUploadModal" class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4">
      <div class="w-full max-w-md rounded-xl bg-white shadow-xl">
        <div class="flex items-center justify-between border-b border-gray-200 px-6 py-4">
          <h2 class="text-lg font-semibold text-gray-900">{{ t('admin.documents.uploadFile') }}</h2>
          <button type="button" class="rounded p-1 text-gray-400 hover:text-gray-700" @click="showUploadModal = false">
            <X class="h-5 w-5" />
          </button>
        </div>
        <div class="space-y-4 px-6 py-5">
          <div>
            <label class="block text-sm font-medium text-gray-700">{{ t('admin.documents.selectFile') }}</label>
            <input
              type="file"
              class="mt-1 block w-full text-sm text-gray-700 file:mr-3 file:rounded-md file:border file:border-gray-300 file:bg-white file:px-3 file:py-1.5 file:text-sm file:font-medium file:text-gray-700 hover:file:bg-gray-50"
              @change="handleFileChange"
            />
          </div>
          <div>
            <label class="block text-sm font-medium text-gray-700">{{ t('admin.documents.title') }}</label>
            <input
              v-model="uploadTitle"
              type="text"
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>
          <div>
            <label class="block text-sm font-medium text-gray-700">{{ t('admin.documents.visibility') }}</label>
            <select
              v-model="uploadVisibility"
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            >
              <option v-for="opt in visibilityOptions" :key="opt.value" :value="opt.value">{{ t(opt.labelKey) }}</option>
            </select>
          </div>
          <p v-if="uploadError" class="text-sm text-red-600">{{ uploadError }}</p>
        </div>
        <div class="flex justify-end gap-2 border-t border-gray-200 px-6 py-4">
          <button
            type="button"
            class="rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
            @click="showUploadModal = false"
          >
            {{ t('common.cancel') }}
          </button>
          <button
            type="button"
            :disabled="!uploadFile || uploading"
            class="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-blue-700 disabled:opacity-50"
            @click="uploadDocument()"
          >
            {{ uploading ? t('common.uploading') : t('admin.documents.uploadFile') }}
          </button>
        </div>
      </div>
    </div>

    <!-- Authored document modal -->
    <div v-if="showAuthoredModal" class="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/40 p-4 pt-12">
      <div class="w-full max-w-3xl rounded-xl bg-white shadow-xl">
        <div class="flex items-center justify-between border-b border-gray-200 px-6 py-4">
          <h2 class="text-lg font-semibold text-gray-900">
            {{ editingDocID ? t('admin.documents.editDocument') : t('admin.documents.createDocument') }}
          </h2>
          <button type="button" class="rounded p-1 text-gray-400 hover:text-gray-700" @click="showAuthoredModal = false">
            <X class="h-5 w-5" />
          </button>
        </div>
        <div class="space-y-4 px-6 py-5">
          <div>
            <label class="block text-sm font-medium text-gray-700">{{ t('admin.documents.title') }}</label>
            <input
              v-model="authoredTitle"
              type="text"
              class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>
          <div class="grid grid-cols-2 gap-4">
            <div>
              <label class="block text-sm font-medium text-gray-700">{{ t('admin.documents.visibility') }}</label>
              <select
                v-model="authoredVisibility"
                class="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              >
                <option v-for="opt in visibilityOptions" :key="opt.value" :value="opt.value">{{ t(opt.labelKey) }}</option>
              </select>
            </div>
            <div class="flex items-end pb-1">
              <label class="flex items-center gap-2 text-sm font-medium text-gray-700">
                <input v-model="authoredPublished" type="checkbox" class="rounded border-gray-300" />
                {{ t('admin.documents.publishNow') }}
              </label>
            </div>
          </div>
          <div>
            <label class="mb-1 block text-sm font-medium text-gray-700">{{ t('admin.documents.content') }}</label>
            <RichEditor v-model="authoredBody" />
          </div>
          <p v-if="authoredError" class="text-sm text-red-600">{{ authoredError }}</p>
        </div>
        <div class="flex justify-end gap-2 border-t border-gray-200 px-6 py-4">
          <button
            type="button"
            class="rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
            @click="showAuthoredModal = false"
          >
            {{ t('common.cancel') }}
          </button>
          <button
            type="button"
            :disabled="!authoredTitle.trim() || savingAuthored"
            class="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-blue-700 disabled:opacity-50"
            @click="saveAuthoredDoc()"
          >
            {{ savingAuthored ? t('common.saving') : t('common.save') }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
