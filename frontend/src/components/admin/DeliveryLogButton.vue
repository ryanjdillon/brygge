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

type SendStatus =
  | 'accepted'
  | 'deferred'
  | 'rejected'
  | 'queued'
  | 'no_record'
  | 'pending'
  | 'beyond_retention'
  | 'unavailable'

interface DeliveryEvent {
  timestamp: string
  message: string
  hostname?: string
  smtp_code?: number
  details?: string
}

interface DeliverySend {
  kind: 'sent' | 'resent' | 'reminder'
  at: string
  status: SendStatus
  smtp_code?: number
  remote_host?: string
  details?: string
  next_retry_at?: string
  events: DeliveryEvent[]
}

const open = ref(false)
const sends = ref<DeliverySend[]>([])
const recipient = ref<string | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)
const noRecipient = ref(false)

async function load() {
  loading.value = true
  error.value = null
  noRecipient.value = false
  try {
    const res = await totpAwareFetch(`/api/v1/admin/financials/invoices/${props.invoiceId}/delivery-log`)
    if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
    const body = await res.json()
    sends.value = body.sends ?? []
    recipient.value = body.recipient_email ?? null
    noRecipient.value = body.reason === 'no_recipient'
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : 'Failed'
  } finally {
    loading.value = false
  }
}

// Reload on every open: a send shown as just sent or queued moves on within
// minutes.
async function activate() {
  open.value = true
  await load()
}

function formatTime(iso: string): string {
  return fmtDateTime(iso, locale.value)
}

const statusClass: Record<SendStatus, string> = {
  accepted: 'bg-emerald-50 text-emerald-800 ring-emerald-200',
  deferred: 'bg-amber-50 text-amber-800 ring-amber-200',
  rejected: 'bg-red-50 text-red-800 ring-red-200',
  no_record: 'bg-red-50 text-red-800 ring-red-200',
  queued: 'bg-gray-50 text-gray-700 ring-gray-200',
  pending: 'bg-gray-50 text-gray-700 ring-gray-200',
  beyond_retention: 'bg-gray-50 text-gray-700 ring-gray-200',
  unavailable: 'bg-gray-50 text-gray-700 ring-gray-200',
}

const statusWithHelp = new Set<SendStatus>(['deferred', 'rejected', 'no_record', 'pending', 'beyond_retention', 'unavailable'])

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

  <Modal v-model:open="open" size="2xl" :padding="false" full-screen-on-mobile>
    <template #header>
      <div class="min-w-0">
        <h2 class="text-base font-semibold text-gray-900">{{ t('admin.faktura.sent.deliveryLog.title') }}</h2>
        <p v-if="recipient" class="mt-0.5 break-all font-mono text-xs text-gray-500">{{ recipient }}</p>
      </div>
    </template>

    <div class="max-h-[70vh] overflow-y-auto px-5 py-3">
      <p v-if="loading" class="py-6 text-center text-sm text-gray-500">{{ t('common.loading') }}…</p>
      <p v-else-if="error" class="rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">{{ error }}</p>
      <p v-else-if="noRecipient" class="py-6 text-center text-sm text-gray-500">{{ t('admin.faktura.sent.deliveryLog.noRecipient') }}</p>
      <p v-else-if="sends.length === 0" class="py-6 text-center text-sm text-gray-500">{{ t('admin.faktura.sent.deliveryLog.noSends') }}</p>
      <ul v-else class="space-y-4">
        <li v-for="(send, i) in sends" :key="i" class="rounded-lg border border-gray-200 p-3 text-sm" data-testid="delivery-send">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <span class="font-medium text-gray-900">
              {{ t(`admin.faktura.sent.deliveryLog.kind.${send.kind}`) }}
              <span class="font-normal tabular-nums text-gray-500">· {{ formatTime(send.at) }}</span>
            </span>
            <span
              class="rounded-full px-2 py-0.5 text-xs font-semibold ring-1 ring-inset"
              :class="statusClass[send.status]"
              data-testid="delivery-status"
            >
              {{ t(`admin.faktura.sent.deliveryLog.status.${send.status}`) }}
            </span>
          </div>
          <p v-if="statusWithHelp.has(send.status)" class="mt-1.5 text-xs leading-snug text-gray-600">
            {{ t(`admin.faktura.sent.deliveryLog.statusHelp.${send.status}`) }}
          </p>
          <p v-if="send.status === 'deferred' && send.next_retry_at" class="mt-1 text-xs text-gray-600">
            {{ t('admin.faktura.sent.deliveryLog.nextRetry', { time: formatTime(send.next_retry_at) }) }}
          </p>
          <ul v-if="send.events.length" class="mt-2 divide-y divide-gray-100 border-t border-gray-100">
            <li v-for="(ev, j) in send.events" :key="j" class="py-2">
              <div class="flex items-center justify-between gap-3">
                <span class="text-xs tabular-nums text-gray-500">{{ formatTime(ev.timestamp) }}</span>
                <span
                  v-if="ev.smtp_code"
                  class="rounded px-2 py-0.5 font-mono text-xs font-semibold"
                  :class="codeClass(ev.smtp_code)"
                >
                  {{ ev.smtp_code }}
                </span>
              </div>
              <p class="mt-0.5 text-xs text-gray-800">{{ ev.message }}</p>
              <p v-if="ev.hostname" class="mt-0.5 break-all font-mono text-xs text-gray-600">{{ ev.hostname }}</p>
              <p v-if="ev.details" class="mt-0.5 break-words font-mono text-xs text-gray-400">{{ ev.details }}</p>
            </li>
          </ul>
        </li>
      </ul>
    </div>

    <template #footer>
      <p class="text-xs leading-snug text-gray-500">{{ t('admin.faktura.sent.deliveryLog.helpText') }}</p>
    </template>
  </Modal>
</template>
