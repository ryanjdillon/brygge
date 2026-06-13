<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { MailSearch, X } from 'lucide-vue-next'
import { useFreshTotp } from '@/composables/useFreshTotp'

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

async function toggle() {
  open.value = !open.value
  if (open.value && !fetched.value) {
    await load()
  }
}

function close() {
  open.value = false
}

function formatTime(iso: string): string {
  return new Date(iso).toLocaleString(locale.value || 'nb-NO')
}

const codeClass = computed(() => (code: number | undefined): string => {
  if (!code) return 'text-gray-500'
  if (code >= 200 && code < 300) return 'text-emerald-700 bg-emerald-50'
  if (code >= 400 && code < 500) return 'text-amber-700 bg-amber-50'
  if (code >= 500) return 'text-red-700 bg-red-50'
  return 'text-gray-700 bg-gray-50'
})
</script>

<template>
  <div class="relative inline-block">
    <button
      type="button"
      class="text-gray-400 hover:text-gray-700"
      :title="t('admin.faktura.sent.deliveryLog.title')"
      :aria-label="t('admin.faktura.sent.deliveryLog.title')"
      @click="toggle"
    >
      <MailSearch class="h-4 w-4" />
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
        class="absolute right-0 z-30 mt-1 w-[28rem] rounded-md border border-gray-200 bg-white shadow-lg"
        role="dialog"
      >
        <div class="flex items-start justify-between gap-2 border-b border-gray-100 px-3 py-2">
          <div>
            <p class="text-xs font-semibold text-gray-700">{{ t('admin.faktura.sent.deliveryLog.title') }}</p>
            <p v-if="recipient" class="text-[10px] text-gray-500 font-mono">{{ recipient }}</p>
          </div>
          <button
            type="button"
            class="text-gray-400 hover:text-gray-700"
            :aria-label="t('common.close')"
            @click="close"
          >
            <X class="h-3.5 w-3.5" />
          </button>
        </div>

        <div class="max-h-96 overflow-y-auto p-2">
          <p v-if="loading" class="px-2 py-3 text-xs text-gray-500">{{ t('common.loading') }}…</p>
          <p v-else-if="error" class="px-2 py-3 text-xs text-red-700">{{ error }}</p>
          <p v-else-if="noRecipient" class="px-2 py-3 text-xs text-gray-500">{{ t('admin.faktura.sent.deliveryLog.noRecipient') }}</p>
          <p v-else-if="items.length === 0" class="px-2 py-3 text-xs text-gray-500">{{ t('admin.faktura.sent.deliveryLog.empty') }}</p>
          <ul v-else class="divide-y divide-gray-100">
            <li v-for="(entry, i) in items" :key="i" class="px-2 py-2 text-xs">
              <div class="flex items-center justify-between gap-2">
                <span class="font-medium text-gray-900 tabular-nums">{{ formatTime(entry.timestamp) }}</span>
                <span
                  v-if="entry.smtp_code"
                  class="rounded px-1.5 py-0.5 font-mono text-[10px] font-semibold"
                  :class="codeClass(entry.smtp_code)"
                >
                  {{ entry.smtp_code }}
                </span>
              </div>
              <p v-if="entry.destination" class="mt-0.5 truncate text-gray-600 font-mono">
                {{ entry.destination }}
              </p>
              <p class="mt-1 truncate text-[10px] text-gray-400 font-mono" :title="entry.raw">
                {{ entry.raw }}
              </p>
            </li>
          </ul>
        </div>

        <div class="border-t border-gray-100 px-3 py-2 text-[10px] leading-snug text-gray-500">
          {{ t('admin.faktura.sent.deliveryLog.helpText') }}
        </div>
      </div>
    </Transition>
  </div>
</template>
