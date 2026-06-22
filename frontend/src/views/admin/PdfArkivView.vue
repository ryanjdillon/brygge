<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Search, AlertCircle } from 'lucide-vue-next'
import FakturaArchiveButton from '@/components/admin/FakturaArchiveButton.vue'
import { formatNOK, formatDateTime as fmtDateTime } from '@/lib/format'

defineProps<{ embedded?: boolean }>()

interface LookupResult {
  id: string
  invoice_number: number
  member_name: string
  member_email: string
  total_amount: number
  issue_date: string
  archive_count: number
}

const { t, locale } = useI18n()

const query = ref('')
const result = ref<LookupResult | null>(null)
const archive = ref<{ id: string; archived_at: string; reason: string; archived_by: string; bytes: number }[]>([])
const loading = ref(false)
const error = ref<string | null>(null)

const queryClean = computed(() => query.value.trim().replace(/^#/, ''))

async function lookup() {
  if (!queryClean.value) {
    error.value = t('admin.pdfArchive.queryRequired')
    return
  }
  loading.value = true
  error.value = null
  result.value = null
  archive.value = []
  try {
    // Reuse the existing invoice list with a hand-built filter so
    // we don't need a new endpoint just to look up by number.
    const res = await fetch(`/api/v1/admin/financials/invoices?status=sent`, { credentials: 'include' })
    if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
    const body = await res.json()
    const items: LookupResult[] = body.items ?? []
    const match = items.find((d) => String(d.invoice_number) === queryClean.value)
    if (!match) {
      // Fall back to voided + draft tabs since the operator may not
      // know which list the invoice lives on.
      for (const tab of ['voided', 'draft'] as const) {
        const r2 = await fetch(`/api/v1/admin/financials/invoices?status=${tab}`, { credentials: 'include' })
        if (!r2.ok) continue
        const b2 = await r2.json()
        const m2 = (b2.items ?? []).find((d: LookupResult) => String(d.invoice_number) === queryClean.value)
        if (m2) {
          result.value = m2
          break
        }
      }
      if (!result.value) {
        error.value = t('admin.pdfArchive.notFound', { n: queryClean.value })
        return
      }
    } else {
      result.value = match
    }
    // Load archive entries for the matched invoice.
    const ar = await fetch(`/api/v1/admin/financials/invoices/${result.value!.id}/pdf-archive`, { credentials: 'include' })
    if (ar.ok) {
      const body = await ar.json()
      archive.value = body.items ?? []
    }
  } catch (e: any) {
    error.value = e?.message ?? 'Failed'
  } finally {
    loading.value = false
  }
}

function formatDate(iso: string): string {
  return fmtDateTime(iso, locale.value)
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} kB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}

</script>

<template>
  <div>
    <template v-if="!embedded">
      <h1 class="text-2xl font-bold text-gray-900">{{ t('admin.pdfArchive.title') }}</h1>
      <p class="mt-1 text-sm text-gray-600">{{ t('admin.pdfArchive.subtitle') }}</p>
    </template>

    <form :class="['flex max-w-md items-center gap-2', embedded ? 'mt-0' : 'mt-6']" @submit.prevent="lookup">
      <div class="relative flex-1">
        <Search class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" />
        <input
          v-model="query"
          type="search"
          inputmode="numeric"
          :placeholder="t('admin.pdfArchive.placeholder')"
          class="w-full rounded-md border border-gray-300 py-2 pl-9 pr-3 text-sm focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
        />
      </div>
      <button
        type="submit"
        :disabled="loading"
        class="rounded-md bg-brand-600 px-4 py-2 text-sm font-semibold text-white hover:bg-brand-700 disabled:opacity-50"
      >
        {{ loading ? t('common.loading') : t('admin.pdfArchive.lookup') }}
      </button>
    </form>

    <div v-if="error" class="mt-4 flex items-start gap-2 rounded-md bg-amber-50 px-3 py-2 text-sm text-amber-800">
      <AlertCircle class="mt-0.5 h-4 w-4 shrink-0" />
      <p>{{ error }}</p>
    </div>

    <div v-if="result" class="mt-6 rounded-md border border-gray-200 bg-white p-5">
      <div class="flex items-center justify-between gap-4">
        <div>
          <p class="text-xs font-medium uppercase tracking-wide text-gray-500">{{ t('admin.pdfArchive.invoice') }}</p>
          <p class="mt-1 text-lg font-semibold text-gray-900 tabular-nums">#{{ result.invoice_number }}</p>
        </div>
        <div class="text-right">
          <p class="text-sm font-medium text-gray-900">{{ result.member_name || '—' }}</p>
          <p class="text-xs text-gray-500">{{ result.member_email || '—' }}</p>
        </div>
      </div>
      <div class="mt-4 grid grid-cols-2 gap-x-6 gap-y-2 border-t border-gray-100 pt-4 text-sm">
        <div>
          <p class="text-xs text-gray-500">{{ t('admin.pdfArchive.amount') }}</p>
          <p class="font-medium tabular-nums text-gray-900">{{ formatNOK(result.total_amount) }}</p>
        </div>
        <div>
          <p class="text-xs text-gray-500">{{ t('admin.pdfArchive.issueDate') }}</p>
          <p class="font-medium text-gray-900">{{ result.issue_date }}</p>
        </div>
      </div>

      <div class="mt-6 flex items-center gap-3 border-t border-gray-100 pt-4">
        <a
          :href="`/api/v1/admin/financials/invoices/${result.id}/pdf`"
          target="_blank"
          rel="noopener"
          class="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-semibold text-gray-700 hover:bg-gray-50"
        >
          {{ t('admin.pdfArchive.viewCurrent') }}
        </a>
        <FakturaArchiveButton :invoice-id="result.id" />
        <span class="ml-2 text-xs text-gray-500">
          {{ t('admin.pdfArchive.archiveCount', { n: archive.length }) }}
        </span>
      </div>

      <div v-if="archive.length > 0" class="mt-4 border-t border-gray-100 pt-4">
        <p class="mb-2 text-xs font-semibold uppercase tracking-wide text-gray-500">
          {{ t('admin.faktura.archive.title') }}
        </p>
        <ul class="divide-y divide-gray-100">
          <li v-for="entry in archive" :key="entry.id" class="py-2 text-sm">
            <div class="flex items-center justify-between">
              <span class="font-medium tabular-nums text-gray-900">{{ formatDate(entry.archived_at) }}</span>
              <div class="flex items-center gap-3">
                <span class="text-xs text-gray-400">{{ formatBytes(entry.bytes) }}</span>
                <a
                  :href="`/api/v1/admin/financials/invoices/${result.id}/pdf-archive/${entry.id}`"
                  target="_blank"
                  rel="noopener"
                  class="text-xs font-semibold text-brand-700 hover:underline"
                >
                  {{ t('admin.faktura.archive.view') }}
                </a>
                <a
                  :href="`/api/v1/admin/financials/invoices/${result.id}/pdf-archive/${entry.id}?download=1`"
                  class="text-xs font-semibold text-brand-700 hover:underline"
                >
                  {{ t('admin.faktura.archive.download') }}
                </a>
              </div>
            </div>
            <p class="mt-0.5 text-xs text-gray-500">
              {{ t(`admin.faktura.archive.reason.${entry.reason}`, entry.reason) }}
              <span v-if="entry.archived_by"> · {{ entry.archived_by }}</span>
            </p>
          </li>
        </ul>
      </div>
    </div>
  </div>
</template>
