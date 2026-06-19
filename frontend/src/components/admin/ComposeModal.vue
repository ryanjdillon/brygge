<script setup lang="ts">
import { ref, computed } from 'vue'
import { Send } from 'lucide-vue-next'
import Modal from '@/components/ui/Modal.vue'
import RichEditor from '@/components/ui/RichEditor.vue'
import RecipientPicker, { type RecipientValue } from './RecipientPicker.vue'
import { useApi } from '@/composables/useApi'

interface MailboxView {
  address: string
  display_name: string
  can_send_as: boolean
}

const props = defineProps<{
  mailboxes: MailboxView[]
}>()

const emit = defineEmits<{
  (e: 'close'): void
}>()

const { fetchApi } = useApi()

function htmlToText(html: string): string {
  const el = document.createElement('div')
  el.innerHTML = html
  return el.innerText
}

const step = ref<'compose' | 'preview'>('compose')
const sending = ref(false)
const error = ref<string | null>(null)
const subject = ref('')
const body = ref('')
const recipients = ref<RecipientValue>({ groups: [], individuals: [] })

const sendableMailboxes = computed(() => props.mailboxes.filter((m) => m.can_send_as))

const defaultFrom = computed(
  () =>
    sendableMailboxes.value.find((m) => m.address.startsWith('post@')) ??
    sendableMailboxes.value[0] ??
    null,
)
const fromAddress = ref<string>(defaultFrom.value?.address ?? '')

const canPreview = computed(() => {
  const hasRecipient =
    recipients.value.groups.length > 0 || recipients.value.individuals.length > 0
  return hasRecipient && (subject.value.trim() || body.value.trim())
})

const recipientSummary = computed(() => {
  const parts: string[] = []
  const labelMap: Record<string, string> = {
    all: 'Alle',
    members: 'Medlemar',
    board: 'Styremedlemar',
    slip_holders: 'Plasseigarar',
    waiting_list: 'Venteliste',
  }
  for (const g of recipients.value.groups) parts.push(labelMap[g] ?? g)
  for (const ind of recipients.value.individuals) parts.push(ind.name || ind.email)
  return parts.join(', ')
})

const selectedFromLabel = computed(() => {
  const m = sendableMailboxes.value.find((m) => m.address === fromAddress.value)
  return m ? `${m.display_name} <${m.address}>` : fromAddress.value
})

async function send() {
  if (sending.value) return
  sending.value = true
  error.value = null
  try {
    await fetchApi(
      `/api/v1/admin/inbox/${encodeURIComponent(fromAddress.value)}/send`,
      {
        method: 'POST',
        body: JSON.stringify({
          bcc_groups: recipients.value.groups,
          bcc: recipients.value.individuals.map((i) => ({ name: i.name, email: i.email })),
          subject: subject.value,
          body_html: body.value,
          body_text: htmlToText(body.value),
        }),
      },
    )
    emit('close')
  } catch (e) {
    error.value = (e as Error)?.message ?? 'Sending feila. Prøv igjen.'
  } finally {
    sending.value = false
  }
}

function tryClose() {
  if (subject.value.trim() || body.value.trim()) {
    if (!confirm('Forkast kladden?')) return
  }
  emit('close')
}
</script>

<template>
  <Modal :open="true" size="3xl" :show-close-button="false" @close="tryClose">
    <template #header>
      <h2 class="text-base font-semibold text-gray-900">Ny melding</h2>
      <button
        type="button"
        class="ml-auto text-gray-400 hover:text-gray-700"
        @click="tryClose"
      >
        <span class="sr-only">Lukk</span>
        <svg class="h-5 w-5" viewBox="0 0 20 20" fill="currentColor">
          <path d="M6.28 5.22a.75.75 0 0 0-1.06 1.06L8.94 10l-3.72 3.72a.75.75 0 1 0 1.06 1.06L10 11.06l3.72 3.72a.75.75 0 1 0 1.06-1.06L11.06 10l3.72-3.72a.75.75 0 0 0-1.06-1.06L10 8.94 6.28 5.22Z" />
        </svg>
      </button>
    </template>

    <!-- Compose step -->
    <div v-if="step === 'compose'" class="space-y-4">
      <!-- Fra -->
      <div>
        <label class="block text-xs font-medium uppercase tracking-wide text-gray-500">Fra</label>
        <select
          v-model="fromAddress"
          class="mt-1 w-full rounded-md border border-gray-300 px-3 py-1.5 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        >
          <option v-for="m in sendableMailboxes" :key="m.address" :value="m.address">
            {{ m.display_name }} &lt;{{ m.address }}&gt;
          </option>
        </select>
      </div>

      <!-- Mottakarar -->
      <div>
        <label class="block text-xs font-medium uppercase tracking-wide text-gray-500">Mottakarar</label>
        <div class="mt-1">
          <RecipientPicker v-model="recipients" />
        </div>
      </div>

      <!-- Emne -->
      <div>
        <label class="block text-xs font-medium uppercase tracking-wide text-gray-500">Emne</label>
        <input
          v-model="subject"
          type="text"
          class="mt-1 w-full rounded-md border border-gray-300 px-3 py-1.5 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
        />
      </div>

      <!-- Melding -->
      <div>
        <label class="block text-xs font-medium uppercase tracking-wide text-gray-500">Melding</label>
        <div class="mt-1">
          <RichEditor v-model="body" />
        </div>
      </div>

      <div class="flex justify-end">
        <button
          type="button"
          :disabled="!canPreview"
          class="inline-flex items-center gap-2 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40"
          @click="step = 'preview'"
        >
          Førehandsvis
        </button>
      </div>
    </div>

    <!-- Preview step -->
    <div v-else class="space-y-4">
      <div class="rounded-lg border border-gray-200 bg-gray-50 p-5 text-sm">
        <div class="mb-1 text-gray-500">
          <span class="font-medium text-gray-700">Fra:</span> {{ selectedFromLabel }}
        </div>
        <div class="mb-3 text-gray-500">
          <span class="font-medium text-gray-700">Til (BCC):</span> {{ recipientSummary }}
        </div>
        <h3 class="text-base font-semibold text-gray-900">{{ subject }}</h3>
        <p class="mt-3 whitespace-pre-wrap text-gray-700">{{ body }}</p>
      </div>

      <div v-if="error" class="rounded border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-700">
        {{ error }}
      </div>

      <div class="flex justify-end gap-3">
        <button
          type="button"
          class="rounded-md border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
          :disabled="sending"
          @click="step = 'compose'"
        >
          Rediger
        </button>
        <button
          type="button"
          :disabled="sending"
          class="inline-flex items-center gap-2 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
          @click="send"
        >
          <Send class="h-4 w-4" />
          {{ sending ? 'Sender…' : 'Send' }}
        </button>
      </div>
    </div>
  </Modal>
</template>
