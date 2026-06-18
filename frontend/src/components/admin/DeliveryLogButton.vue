<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { MailSearch } from 'lucide-vue-next'
import { useFreshTotp } from '@/composables/useFreshTotp'
import Modal from '@/components/ui/Modal.vue'
import { formatDateTime as fmtDateTime } from '@/lib/format'

const props = defineProps<{ invoiceId: string }>()

const { t, locale } = useI18n()
const { totpAwareFetch } = useFreshTotp()

interface DeliveryEntry {
  timestamp: string
  destination?: string
  smtp_code?: number
  raw: string
}

const open = ref(false)
const items = ref<DeliveryEntry[]>([])
const recipient = ref<string | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)
const fetched = ref(false)
const noRecipient = ref(false)

async function load() {
  loading.value = true
  error.value = null
  noRecipient.value = false
  try {
    const res = await totpAwareFetch(`/api/v1/admin/financials/invoices/${props.invoiceId}/delivery-log`)
    if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
    const body = await res.json()
    items.value = body.items ?? []
    recipient.value = body.recipient_email ?? null
    noRecipient.value = body.reason === 'no_recipient'
    fetched.value = true
  } catch (e: any) {
    error.value = e?.message ?? 'Failed'
  } finally {
    loading.value = false
  }
}

async function activate() {
  open.value = true
  if (!fetched.value) await load()
}

function formatTime(iso: string): string {
  return fmtDateTime(iso, locale.value)
}

function codeClass(code: number | undefined): string {
  if (!code) return 'text-gray-500'
  if (code >= 200 && code < 300) return 'text-emerald-700 bg-emerald-50'
  if (code >= 400 && code < 500) return 'text-amber-700 bg-amber-50'
  if (code >= 500) return 'text-red-700 bg-red-50'
  return 'text-gray-700 bg-gray-50'
}
</script>

<template>
  <button
    type="button"
    class="text-gray-400 hover:text-gray-700"
    :title="t('admin.faktura.sent.deliveryLog.title')"
    :aria-label="t('admin.faktura.sent.deliveryLog.title')"
    @click="activate"
  >
    <MailSearch class="h-4 w-4" />
  </button>

  <Modal v-model:open="open" size="2xl" :padding="false">
    <template #header>
      <div>
        <h2 class="text-base font-semibold text-gray-900">{{ t('admin.faktura.sent.deliveryLog.title') }}</h2>
        <p v-if="recipient" class="mt-0.5 font-mono text-xs text-gray-500">{{ recipient }}</p>
      </div>
    </template>

    <div class="max-h-[60vh] overflow-y-auto px-5 py-3">
      <p v-if="loading" class="py-6 text-center text-sm text-gray-500">{{ t('common.loading') }}…</p>
      <p v-else-if="error" class="rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">{{ error }}</p>
      <p v-else-if="noRecipient" class="py-6 text-center text-sm text-gray-500">{{ t('admin.faktura.sent.deliveryLog.noRecipient') }}</p>
      <p v-else-if="items.length === 0" class="py-6 text-center text-sm text-gray-500">{{ t('admin.faktura.sent.deliveryLog.empty') }}</p>
      <ul v-else class="divide-y divide-gray-100">
        <li v-for="(entry, i) in items" :key="i" class="py-3 text-sm">
          <div class="flex items-center justify-between gap-3">
            <span class="font-medium text-gray-900 tabular-nums">{{ formatTime(entry.timestamp) }}</span>
            <span
              v-if="entry.smtp_code"
              class="rounded px-2 py-0.5 font-mono text-xs font-semibold"
              :class="codeClass(entry.smtp_code)"
            >
              {{ entry.smtp_code }}
            </span>
          </div>
          <p v-if="entry.destination" class="mt-1 truncate font-mono text-xs text-gray-600">
            {{ entry.destination }}
          </p>
          <p class="mt-1 break-all font-mono text-xs text-gray-400">{{ entry.raw }}</p>
        </li>
      </ul>
    </div>

    <template #footer>
      <p class="text-xs leading-snug text-gray-500">{{ t('admin.faktura.sent.deliveryLog.helpText') }}</p>
    </template>
  </Modal>
</template>
