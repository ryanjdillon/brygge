<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Download, Trash2, ShieldCheck, FileText } from 'lucide-vue-next'
import { RouterLink } from 'vue-router'
import { useDataExport, useDeletionStatus, useRequestDeletion, useCancelDeletion, useMyConsents } from '@/composables/useGdpr'
import { formatDate as fmtDate } from '@/lib/format'

const { t, locale } = useI18n()
const { data: deletionRequest, isLoading: deletionLoading } = useDeletionStatus()
const { mutate: requestDeletion, isPending: requesting } = useRequestDeletion()
const { mutate: cancelDeletion, isPending: cancelling } = useCancelDeletion()
const { mutate: exportData, isPending: exporting } = useDataExport()
const { consents, isLoading: consentsLoading } = useMyConsents()

const hasPendingDeletion = computed(() => deletionRequest.value?.status === 'pending')

function confirmDelete() {
  if (confirm(t('gdpr.deleteConfirm'))) {
    requestDeletion()
  }
}

function formatDate(d: string): string {
  return fmtDate(d, locale.value)
}
</script>

<template>
  <div class="space-y-6">
    <div>
      <h2 class="text-xl font-semibold text-gray-900">{{ t('gdpr.title') }}</h2>
      <p class="mt-1 text-sm text-gray-600">{{ t('gdpr.subtitle') }}</p>
    </div>

    <!-- Privacy Policy link -->
    <RouterLink
      to="/portal/privacy-policy"
      class="flex items-center justify-between rounded-lg border border-gray-200 bg-white p-4 transition hover:border-blue-300 hover:shadow-sm"
    >
      <div class="flex items-center gap-3">
        <FileText class="h-5 w-5 text-blue-600" />
        <div>
          <p class="font-medium text-gray-900">{{ t('gdpr.privacyPolicyTitle') }}</p>
          <p class="text-sm text-gray-500">{{ t('gdpr.privacyPolicyDescription') }}</p>
        </div>
      </div>
      <span class="text-sm text-blue-600">{{ t('common.view') }} &rarr;</span>
    </RouterLink>

    <!-- Data Export -->
    <div class="rounded-lg border border-gray-200 bg-white p-4">
      <div class="flex items-center justify-between">
        <div class="flex items-center gap-3">
          <Download class="h-5 w-5 text-blue-600" />
          <div>
            <p class="font-medium text-gray-900">{{ t('gdpr.exportTitle') }}</p>
            <p class="text-sm text-gray-500">{{ t('gdpr.exportDescription') }}</p>
          </div>
        </div>
        <button
          :disabled="exporting"
          class="rounded-md bg-blue-600 px-4 py-2 text-sm font-semibold text-white hover:bg-blue-700 disabled:opacity-50"
          @click="exportData()"
        >
          {{ exporting ? t('common.loading') : t('gdpr.exportButton') }}
        </button>
      </div>
    </div>

    <!-- Account Deletion -->
    <div class="rounded-lg border border-gray-200 bg-white p-4">
      <div class="flex items-center gap-3 mb-3">
        <Trash2 class="h-5 w-5 text-red-500" />
        <p class="font-medium text-gray-900">{{ t('gdpr.deleteTitle') }}</p>
      </div>

      <div v-if="deletionLoading" class="animate-pulse h-10 rounded bg-gray-100" />

      <div v-else-if="hasPendingDeletion" class="rounded-md bg-amber-50 p-3">
        <p class="text-sm text-amber-800">
          {{ t('gdpr.deletePending') }}
        </p>
        <p class="mt-1 text-sm text-amber-700">
          {{ t('gdpr.deleteGraceEnd', { date: formatDate(deletionRequest!.grace_end) }) }}
        </p>
        <button
          :disabled="cancelling"
          class="mt-3 rounded-md bg-white border border-gray-300 px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50"
          @click="cancelDeletion()"
        >
          {{ cancelling ? t('common.loading') : t('gdpr.cancelDelete') }}
        </button>
      </div>

      <div v-else>
        <p class="text-sm text-gray-500 mb-3">{{ t('gdpr.deleteDescription') }}</p>
        <button
          :disabled="requesting"
          class="rounded-md bg-red-600 px-4 py-2 text-sm font-semibold text-white hover:bg-red-700 disabled:opacity-50"
          @click="confirmDelete"
        >
          {{ requesting ? t('common.loading') : t('gdpr.deleteButton') }}
        </button>
      </div>
    </div>

    <!-- Consents -->
    <div class="rounded-lg border border-gray-200 bg-white p-4">
      <div class="flex items-center gap-3 mb-3">
        <ShieldCheck class="h-5 w-5 text-green-600" />
        <p class="font-medium text-gray-900">{{ t('gdpr.consentsTitle') }}</p>
      </div>

      <div v-if="consentsLoading" class="animate-pulse space-y-2">
        <div v-for="i in 2" :key="i" class="h-8 rounded bg-gray-100" />
      </div>

      <div v-else-if="consents.length === 0" class="text-sm text-gray-400">
        {{ t('gdpr.noConsents') }}
      </div>

      <ul v-else class="divide-y divide-gray-100">
        <li v-for="c in consents" :key="c.id" class="flex items-center justify-between py-2 text-sm">
          <span class="text-gray-700">{{ t(`gdpr.consentType.${c.consent_type}`) }} v{{ c.version }}</span>
          <span class="text-gray-400">{{ formatDate(c.granted_at) }}</span>
        </li>
      </ul>
    </div>
  </div>
</template>
