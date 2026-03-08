<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAdminDeletionRequests, useProcessDeletion, useAdminLegalDocuments, useCreateLegalDocument } from '@/composables/useGdpr'

const { t } = useI18n()
const { requests, isLoading: requestsLoading } = useAdminDeletionRequests()
const { mutate: processDeletion, isPending: processing } = useProcessDeletion()
const { documents, isLoading: docsLoading } = useAdminLegalDocuments()
const { mutate: createDocument, isPending: creating } = useCreateLegalDocument()

const showForm = ref(false)
const form = ref({
  doc_type: 'terms',
  version: '',
  content: '',
  publish: false,
})

function isGraceExpired(graceEnd: string): boolean {
  return new Date(graceEnd) <= new Date()
}

function formatDate(d: string) {
  return new Date(d).toLocaleDateString()
}

function handleCreate() {
  createDocument(
    { ...form.value },
    {
      onSuccess: () => {
        showForm.value = false
        form.value = { doc_type: 'terms', version: '', content: '', publish: false }
      },
    },
  )
}
</script>

<template>
  <div class="space-y-6">
    <div>
      <h1 class="text-2xl font-bold text-gray-900">{{ t('gdpr.admin.title') }}</h1>
      <p class="mt-1 text-sm text-gray-600">{{ t('gdpr.admin.subtitle') }}</p>
    </div>

    <!-- Deletion Requests -->
    <div>
      <h2 class="text-lg font-semibold text-gray-900 mb-3">{{ t('gdpr.admin.deletionRequests') }}</h2>

      <div v-if="requestsLoading" class="animate-pulse space-y-4">
        <div v-for="i in 3" :key="i" class="h-12 rounded bg-gray-100" />
      </div>

      <div v-else-if="requests.length === 0" class="rounded-lg border border-gray-200 bg-white p-4 text-sm text-gray-400">
        {{ t('gdpr.admin.noPending') }}
      </div>

      <div v-else class="overflow-hidden rounded-lg border border-gray-200 bg-white">
        <table class="min-w-full divide-y divide-gray-200">
          <thead class="bg-gray-50">
            <tr>
              <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
                {{ t('admin.users.name') }}
              </th>
              <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
                {{ t('admin.users.email') }}
              </th>
              <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
                {{ t('gdpr.admin.deletionRequests') }}
              </th>
              <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
                {{ t('gdpr.deleteGraceEnd', { date: '' }) }}
              </th>
              <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
                {{ t('common.actions') }}
              </th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100">
            <tr v-for="req in requests" :key="req.id">
              <td class="px-4 py-3 text-sm font-medium text-gray-900">{{ req.user_name }}</td>
              <td class="px-4 py-3 text-sm text-gray-500">{{ req.user_email }}</td>
              <td class="px-4 py-3 text-sm text-gray-500">{{ formatDate(req.requested_at) }}</td>
              <td class="px-4 py-3 text-sm text-gray-500">{{ formatDate(req.grace_end) }}</td>
              <td class="px-4 py-3">
                <button
                  v-if="isGraceExpired(req.grace_end)"
                  :disabled="processing"
                  class="rounded-md bg-red-600 px-3 py-1.5 text-sm font-semibold text-white hover:bg-red-700 disabled:opacity-50"
                  @click="processDeletion(req.id)"
                >
                  {{ processing ? t('common.loading') : t('gdpr.admin.process') }}
                </button>
                <span v-else class="text-sm text-gray-400">{{ t('gdpr.admin.graceNotExpired') }}</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- Legal Documents -->
    <div>
      <div class="flex items-center justify-between mb-3">
        <h2 class="text-lg font-semibold text-gray-900">{{ t('gdpr.admin.legalDocuments') }}</h2>
        <button
          class="rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white hover:bg-blue-700"
          @click="showForm = !showForm"
        >
          {{ t('gdpr.admin.createDocument') }}
        </button>
      </div>

      <!-- Create Form -->
      <div v-if="showForm" class="rounded-lg border border-gray-200 bg-white p-4 mb-4 space-y-4">
        <div>
          <label class="block text-sm font-medium text-gray-700 mb-1">{{ t('gdpr.admin.docType') }}</label>
          <select
            v-model="form.doc_type"
            class="w-full rounded-md border-gray-300 text-sm"
          >
            <option value="terms">{{ t('gdpr.consentType.terms') }}</option>
            <option value="privacy_policy">{{ t('gdpr.consentType.privacy_policy') }}</option>
          </select>
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700 mb-1">{{ t('gdpr.admin.version') }}</label>
          <input
            v-model="form.version"
            type="text"
            class="w-full rounded-md border-gray-300 text-sm"
            placeholder="1.0"
          />
        </div>
        <div>
          <label class="block text-sm font-medium text-gray-700 mb-1">{{ t('gdpr.admin.content') }}</label>
          <textarea
            v-model="form.content"
            rows="6"
            class="w-full rounded-md border-gray-300 text-sm"
          />
        </div>
        <div class="flex items-center gap-2">
          <input
            id="publish"
            v-model="form.publish"
            type="checkbox"
            class="rounded border-gray-300 text-blue-600"
          />
          <label for="publish" class="text-sm text-gray-700">{{ t('gdpr.admin.publish') }}</label>
        </div>
        <button
          :disabled="creating || !form.version || !form.content"
          class="rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white hover:bg-blue-700 disabled:opacity-50"
          @click="handleCreate"
        >
          {{ creating ? t('common.loading') : t('gdpr.admin.save') }}
        </button>
      </div>

      <div v-if="docsLoading" class="animate-pulse space-y-4">
        <div v-for="i in 2" :key="i" class="h-12 rounded bg-gray-100" />
      </div>

      <div v-else-if="documents.length === 0" class="rounded-lg border border-gray-200 bg-white p-4 text-sm text-gray-400">
        {{ t('gdpr.admin.noDocuments') }}
      </div>

      <div v-else class="overflow-hidden rounded-lg border border-gray-200 bg-white">
        <table class="min-w-full divide-y divide-gray-200">
          <thead class="bg-gray-50">
            <tr>
              <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
                {{ t('gdpr.admin.docType') }}
              </th>
              <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
                {{ t('gdpr.admin.version') }}
              </th>
              <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
                {{ t('admin.documents.date') }}
              </th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100">
            <tr v-for="doc in documents" :key="doc.id">
              <td class="px-4 py-3 text-sm font-medium text-gray-900">
                {{ t(`gdpr.consentType.${doc.doc_type}`) }}
              </td>
              <td class="px-4 py-3 text-sm text-gray-500">v{{ doc.version }}</td>
              <td class="px-4 py-3 text-sm text-gray-500">{{ formatDate(doc.published_at) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </div>
</template>
