<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { History, X } from 'lucide-vue-next'
import { formatDateTime as fmtDateTime } from '@/lib/format'

const props = defineProps<{ invoiceId: string }>()

const { t, locale } = useI18n()

interface ArchiveEntry {
  id: string
  archived_at: string
  reason: string
  archived_by: string
  bytes: number
}

const open = ref(false)
const items = ref<ArchiveEntry[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const fetched = ref(false)

async function load() {
  loading.value = true
  error.value = null
  try {
    const res = await fetch(`/api/v1/admin/financials/invoices/${props.invoiceId}/pdf-archive`, {
      credentials: 'include',
    })
    if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
    const body = await res.json()
    items.value = body.items ?? []
    fetched.value = true
  } catch (e: any) {
    error.value = e?.message ?? 'Failed'
  } finally {
    loading.value = false
  }
}

async function toggle() {
  open.value = !open.value
  if (open.value && !fetched.value) {
    await load()
  }
}

function close() {
  open.value = false
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
  <div class="relative inline-block">
    <button
      type="button"
      class="text-gray-400 hover:text-gray-700"
      :title="t('admin.faktura.archive.title')"
      :aria-label="t('admin.faktura.archive.title')"
      @click="toggle"
    >
      <History class="h-4 w-4" />
    </button>

    <Transition
      enter-active-class="transition duration-100 ease-out"
      enter-from-class="scale-95 opacity-0"
      enter-to-class="scale-100 opacity-100"
      leave-active-class="transition duration-75 ease-in"
      leave-from-class="scale-100 opacity-100"
      leave-to-class="scale-95 opacity-0"
    >
      <div
        v-if="open"
        class="absolute right-0 z-30 mt-1 w-80 rounded-md border border-gray-200 bg-white shadow-lg"
        role="dialog"
      >
        <div class="flex items-center justify-between border-b border-gray-100 px-3 py-2">
          <p class="text-xs font-semibold text-gray-700">{{ t('admin.faktura.archive.title') }}</p>
          <button
            type="button"
            class="text-gray-400 hover:text-gray-700"
            :aria-label="t('common.close')"
            @click="close"
          >
            <X class="h-3.5 w-3.5" />
          </button>
        </div>

        <div class="max-h-72 overflow-y-auto p-2">
          <p v-if="loading" class="px-2 py-3 text-xs text-gray-500">{{ t('common.loading') }}…</p>
          <p v-else-if="error" class="px-2 py-3 text-xs text-red-700">{{ error }}</p>
          <p v-else-if="items.length === 0" class="px-2 py-3 text-xs text-gray-500">{{ t('admin.faktura.archive.empty') }}</p>
          <ul v-else class="divide-y divide-gray-100">
            <li v-for="entry in items" :key="entry.id" class="py-2 px-2 text-xs">
              <div class="flex items-center justify-between gap-2">
                <span class="font-medium text-gray-900 tabular-nums">{{ formatDate(entry.archived_at) }}</span>
                <span class="text-gray-400">{{ formatBytes(entry.bytes) }}</span>
              </div>
              <div class="mt-1 flex items-center justify-between gap-2 text-gray-500">
                <span class="truncate">
                  {{ t(`admin.faktura.archive.reason.${entry.reason}`, entry.reason) }}
                  <span v-if="entry.archived_by"> · {{ entry.archived_by }}</span>
                </span>
                <div class="flex gap-2 shrink-0">
                  <a
                    :href="`/api/v1/admin/financials/invoices/${invoiceId}/pdf-archive/${entry.id}`"
                    target="_blank"
                    rel="noopener"
                    class="font-semibold text-brand-700 hover:underline"
                  >
                    {{ t('admin.faktura.archive.view') }}
                  </a>
                  <a
                    :href="`/api/v1/admin/financials/invoices/${invoiceId}/pdf-archive/${entry.id}?download=1`"
                    class="font-semibold text-brand-700 hover:underline"
                  >
                    {{ t('admin.faktura.archive.download') }}
                  </a>
                </div>
              </div>
            </li>
          </ul>
        </div>
      </div>
    </Transition>
  </div>
</template>
