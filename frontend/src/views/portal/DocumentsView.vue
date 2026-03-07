<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { useApi } from '@/composables/useApi'
import { Download, MessageSquare, ChevronDown, ChevronUp } from 'lucide-vue-next'

const { t } = useI18n()
const { fetchApi } = useApi()
const queryClient = useQueryClient()

interface Document {
  id: string
  title: string
  filename: string
  visibility: string
  created_at: string
}

interface Comment {
  id: string
  author: string
  body: string
  created_at: string
}

const activeFilter = ref('all')
const expandedDoc = ref<string | null>(null)
const commentText = ref('')
const toast = ref<{ type: 'success' | 'error'; message: string } | null>(null)

function showToast(type: 'success' | 'error', message: string) {
  toast.value = { type, message }
  setTimeout(() => (toast.value = null), 3000)
}

const filters = [
  { value: 'all', label: 'portal.documents.filterAll' },
  { value: 'member', label: 'portal.documents.filterMember' },
  { value: 'styre', label: 'portal.documents.filterStyre' },
]

const { data: documents, isLoading, isError } = useQuery({
  queryKey: ['portal', 'documents', activeFilter],
  queryFn: () => {
    const params = activeFilter.value !== 'all' ? `?visibility=${activeFilter.value}` : ''
    return fetchApi<Document[]>(`/api/v1/documents${params}`)
  },
})

const expandedDocId = computed(() => expandedDoc.value)

const {
  data: comments,
  isLoading: commentsLoading,
} = useQuery({
  queryKey: ['portal', 'documents', expandedDocId, 'comments'],
  queryFn: () => fetchApi<Comment[]>(`/api/v1/documents/${expandedDocId.value}/comments`),
  enabled: () => expandedDocId.value !== null,
})

const { mutate: addComment, isPending: isAddingComment } = useMutation({
  mutationFn: (docId: string) =>
    fetchApi<Comment>(`/api/v1/documents/${docId}/comments`, {
      method: 'POST',
      body: JSON.stringify({ body: commentText.value }),
    }),
  onSuccess: (_, docId) => {
    queryClient.invalidateQueries({ queryKey: ['portal', 'documents', docId, 'comments'] })
    commentText.value = ''
    showToast('success', t('portal.documents.commentSuccess'))
  },
  onError: () => {
    showToast('error', t('portal.documents.commentError'))
  },
})

function toggleComments(docId: string) {
  expandedDoc.value = expandedDoc.value === docId ? null : docId
  commentText.value = ''
}

function submitComment(docId: string) {
  if (commentText.value.trim()) {
    addComment(docId)
  }
}
</script>

<template>
  <div>
    <h1 class="text-2xl font-bold text-gray-900">{{ t('portal.documents.title') }}</h1>

    <div
      v-if="toast"
      :class="[
        'mt-4 rounded-md p-3 text-sm',
        toast.type === 'success' ? 'bg-green-50 text-green-800' : 'bg-red-50 text-red-800',
      ]"
    >
      {{ toast.message }}
    </div>

    <div class="mt-6 flex gap-2">
      <button
        v-for="filter in filters"
        :key="filter.value"
        :class="[
          'rounded-md px-3 py-1.5 text-sm font-medium transition',
          activeFilter === filter.value
            ? 'bg-blue-600 text-white'
            : 'bg-gray-100 text-gray-700 hover:bg-gray-200',
        ]"
        @click="activeFilter = filter.value"
      >
        {{ t(filter.label) }}
      </button>
    </div>

    <div v-if="isLoading" class="mt-6 text-gray-500">{{ t('common.loading') }}...</div>

    <div v-else-if="isError" class="mt-6 rounded-md bg-red-50 p-3 text-sm text-red-800">
      {{ t('portal.documents.loadError') }}
    </div>

    <div v-else-if="!documents?.length" class="mt-6 text-gray-500">
      {{ t('portal.documents.noDocuments') }}
    </div>

    <ul v-else class="mt-6 divide-y divide-gray-200">
      <li v-for="doc in documents" :key="doc.id" class="py-4">
        <div class="flex items-center justify-between">
          <div>
            <p class="text-sm font-medium text-gray-900">{{ doc.title }}</p>
            <p class="mt-0.5 text-xs text-gray-500">
              {{ new Date(doc.created_at).toLocaleDateString() }}
              <span class="ml-2 rounded bg-gray-100 px-1.5 py-0.5 text-xs text-gray-600">{{ doc.visibility }}</span>
            </p>
          </div>
          <div class="flex items-center gap-2">
            <a
              :href="`/api/v1/documents/${doc.id}`"
              target="_blank"
              class="flex items-center gap-1 rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm text-gray-700 hover:bg-gray-50"
            >
              <Download class="h-4 w-4" />
              {{ t('portal.documents.download') }}
            </a>
            <button
              class="flex items-center gap-1 rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm text-gray-700 hover:bg-gray-50"
              @click="toggleComments(doc.id)"
            >
              <MessageSquare class="h-4 w-4" />
              {{ t('portal.documents.comments') }}
              <component :is="expandedDoc === doc.id ? ChevronUp : ChevronDown" class="h-3 w-3" />
            </button>
          </div>
        </div>

        <div v-if="expandedDoc === doc.id" class="mt-4 rounded-lg border border-gray-100 bg-gray-50 p-4">
          <div v-if="commentsLoading" class="text-sm text-gray-500">{{ t('common.loading') }}...</div>

          <p v-else-if="!comments?.length" class="text-sm text-gray-500">
            {{ t('portal.documents.noComments') }}
          </p>

          <ul v-else class="space-y-3">
            <li v-for="comment in comments" :key="comment.id" class="rounded-md bg-white p-3 text-sm">
              <div class="flex justify-between">
                <span class="font-medium text-gray-900">{{ comment.author }}</span>
                <span class="text-xs text-gray-400">{{ new Date(comment.created_at).toLocaleDateString() }}</span>
              </div>
              <p class="mt-1 text-gray-700">{{ comment.body }}</p>
            </li>
          </ul>

          <form class="mt-4 flex gap-2" @submit.prevent="submitComment(doc.id)">
            <input
              v-model="commentText"
              type="text"
              :aria-label="t('portal.documents.addComment')"
              :placeholder="t('portal.documents.commentPlaceholder')"
              class="flex-1 rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
            <button
              type="submit"
              :disabled="isAddingComment || !commentText.trim()"
              class="rounded-md bg-blue-600 px-3 py-2 text-sm font-semibold text-white shadow-sm hover:bg-blue-700 disabled:opacity-50"
            >
              {{ t('portal.documents.addComment') }}
            </button>
          </form>
        </div>
      </li>
    </ul>
  </div>
</template>
