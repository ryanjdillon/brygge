<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { Send, X, Trash2, Megaphone, Users } from 'lucide-vue-next'
import RichEditor from '@/components/ui/RichEditor.vue'
import RecipientPicker, { type RecipientValue } from './RecipientPicker.vue'
import { useApi } from '@/composables/useApi'

interface UploadedFile { blobId: string; name: string; size: number; type: string }
interface InlineImage { cid: string; blobId: string; name: string; type: string; src: string }

interface MailboxView {
  address: string
  display_name: string
  from_name: string
  can_send_as: boolean
}

const props = defineProps<{
  mailboxes: MailboxView[]
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'view-broadcasts'): void
}>()

function viewBroadcasts() {
  emit('view-broadcasts')
}

const { fetchApi } = useApi()

function htmlToText(html: string): string {
  const el = document.createElement('div')
  el.innerHTML = html
  return el.innerText
}

const step = ref<'compose' | 'preview'>('compose')
const sending = ref(false)
const error = ref<string | null>(null)
// Set once the send is accepted and queued (202 from the bulk path).
const queued = ref(false)
const queuedCount = ref(0)
const subject = ref('')
const body = ref('')
const recipients = ref<RecipientValue>({ groups: [], individuals: [] })
const editorRef = ref<InstanceType<typeof RichEditor> | null>(null)

// Compose mode (BRY): "standard" puts everyone in the To field (one email,
// recipients see each other); "broadcast" fans out to one individual,
// tracked email per recipient and unlocks group selection. Colour-coded
// blue (standard) / amber (broadcast) throughout.
const mode = ref<'standard' | 'broadcast'>('standard')
function setMode(m: 'standard' | 'broadcast') {
  if (mode.value === m) return
  // Groups are broadcast-only — drop them when leaving broadcast.
  if (m === 'standard' && recipients.value.groups.length) {
    recipients.value = { ...recipients.value, groups: [] }
  }
  mode.value = m
}
const isBroadcast = computed(() => mode.value === 'broadcast')

// Snapshotted before switching to preview (RichEditor unmounts on v-if step change).
const snapshotAttachments = ref<UploadedFile[]>([])
const snapshotInlineImages = ref<InlineImage[]>([])

function goToPreview() {
  snapshotAttachments.value = editorRef.value?.attachments ?? []
  snapshotInlineImages.value = editorRef.value?.inlineImages ?? []
  step.value = 'preview'
}

const sendableMailboxes = computed(() => props.mailboxes.filter((m) => m.can_send_as))
const defaultFrom = computed(
  () =>
    sendableMailboxes.value.find((m) => m.address.startsWith('post@')) ??
    sendableMailboxes.value[0] ??
    null,
)
const fromAddress = ref<string>(defaultFrom.value?.address ?? '')

const hasRecipients = computed(
  () => recipients.value.groups.length > 0 || recipients.value.individuals.length > 0,
)

const canPreview = computed(
  () => hasRecipients.value && Boolean(subject.value.trim() || body.value.trim()),
)

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

// A new compose always addresses groups/individuals (never plain To), so it
// goes out as individual, tracked sends rather than one BCC blast. Surface
// that in the preview so the sender knows what will happen.
const hasGroups = computed(() => recipients.value.groups.length > 0)
const individualCount = computed(() => recipients.value.individuals.length)
const bulkNotice = computed(() => {
  if (hasGroups.value) {
    return `Sendt som individuelle e-postar til kvar mottakar i: ${recipientSummary.value}. Følg leveringa under «Utsendingar».`
  }
  return `Sendt som ${individualCount.value} individuelle e-postar (éin per mottakar). Følg leveringa under «Utsendingar».`
})

const selectedMailbox = computed(
  () => sendableMailboxes.value.find((m) => m.address === fromAddress.value) ?? null,
)

const selectedFromLabel = computed(() => {
  const m = selectedMailbox.value
  return m ? `${m.display_name} <${m.address}>` : fromAddress.value
})

const signatureHtml = computed(() => {
  const m = selectedMailbox.value
  if (!m) return ''
  return [
    '<br>',
    '<hr style="border:none;border-top:1px solid #e5e7eb;margin:16px 0 8px">',
    `<p style="margin:0;font-size:14px;color:#374151">Med venleg helsing,</p>`,
    `<p style="margin:4px 0 0;font-weight:600;color:#111827">${m.from_name}</p>`,
    `<p style="margin:2px 0 0;font-size:13px;color:#6b7280"><a href="mailto:${m.address}" style="color:#2563eb;text-decoration:none">${m.address}</a></p>`,
  ].join('')
})

const signatureText = computed(() => {
  const m = selectedMailbox.value
  if (!m) return ''
  return `\n\n--\nMed venleg helsing,\n${m.from_name}\n${m.address}`
})

async function send() {
  if (sending.value) return
  sending.value = true
  error.value = null
  try {
    let bodyHtml = body.value
    for (const img of snapshotInlineImages.value) {
      bodyHtml = bodyHtml.replaceAll(img.src, `cid:${img.cid}`)
    }
    const indiv = recipients.value.individuals.map((i) => ({ name: i.name, email: i.email }))
    // Standard: everyone in To (one shared email). Broadcast: groups + BCC
    // fan out to individual, tracked sends.
    const recipientFields = isBroadcast.value
      ? { bcc_groups: recipients.value.groups, bcc: indiv }
      : { to: indiv }
    const res = await fetchApi<{ broadcast_id?: string; recipient_count?: number }>(
      `/api/v1/admin/inbox/${encodeURIComponent(fromAddress.value)}/send`,
      {
        method: 'POST',
        body: JSON.stringify({
          ...recipientFields,
          subject: subject.value,
          body_html: bodyHtml + signatureHtml.value,
          body_text: htmlToText(body.value) + signatureText.value,
          attachments: snapshotAttachments.value,
          inline_images: snapshotInlineImages.value.map((img) => ({
            cid: img.cid,
            blob_id: img.blobId,
            name: img.name,
            type: img.type,
          })),
        }),
      },
    )
    // Bulk path returns 202 with a broadcast id — show the queued state and
    // point at the history tab instead of silently closing.
    if (res && res.broadcast_id) {
      queuedCount.value = res.recipient_count ?? 0
      queued.value = true
    } else {
      emit('close')
    }
  } catch (e) {
    error.value = (e as Error)?.message ?? 'Sending feila. Prøv igjen.'
  } finally {
    sending.value = false
  }
}

const confirmingDiscard = ref(false)

function tryClose() {
  if (subject.value.trim() || body.value.trim()) {
    confirmingDiscard.value = true
    return
  }
  emit('close')
}

function discardDraft() {
  confirmingDiscard.value = false
  emit('close')
}

// ── Drag ────────────────────────────────────────────────────────────────────
const pos = ref({ x: 0, y: 0 })
const isDragging = ref(false)
let dragOffset = { x: 0, y: 0 }

onMounted(() => {
  // Center the window in the viewport on open.
  const w = Math.min(Math.max(window.innerWidth * 0.62, 640), 1020)
  const h = Math.min(Math.max(window.innerHeight * 0.82, 520), 940)
  pos.value = {
    x: (window.innerWidth - w) / 2,
    y: (window.innerHeight - h) / 2,
  }
})

function startDrag(e: MouseEvent) {
  if ((e.target as HTMLElement).closest('button')) return
  isDragging.value = true
  dragOffset = { x: e.clientX - pos.value.x, y: e.clientY - pos.value.y }
  window.addEventListener('mousemove', onDrag)
  window.addEventListener('mouseup', stopDrag)
}

function onDrag(e: MouseEvent) {
  pos.value = { x: e.clientX - dragOffset.x, y: e.clientY - dragOffset.y }
}

function stopDrag() {
  isDragging.value = false
  window.removeEventListener('mousemove', onDrag)
  window.removeEventListener('mouseup', stopDrag)
}

onBeforeUnmount(() => {
  window.removeEventListener('mousemove', onDrag)
  window.removeEventListener('mouseup', stopDrag)
})
</script>

<template>
  <Teleport to="body">
    <div
      class="compose-window fixed z-50 flex flex-col rounded-xl border border-gray-200 bg-white shadow-2xl"
      :style="{ left: pos.x + 'px', top: pos.y + 'px' }"
      role="dialog"
      aria-modal="true"
      aria-label="Ny melding"
      @keydown.esc="confirmingDiscard ? (confirmingDiscard = false) : tryClose()"
    >
      <!-- Drag handle / header -->
      <header
        class="flex shrink-0 cursor-grab items-center gap-2 border-b border-gray-200 px-4 py-3 select-none active:cursor-grabbing"
        :class="{ 'cursor-grabbing': isDragging }"
        @mousedown="startDrag"
      >
        <h2 class="text-sm font-semibold text-gray-900">Ny melding</h2>
        <button
          type="button"
          class="ml-auto rounded p-0.5 text-gray-400 hover:bg-gray-100 hover:text-gray-700"
          @click="tryClose"
        >
          <X class="h-4 w-4" />
        </button>
      </header>

      <!-- Discard confirmation banner -->
      <div
        v-if="confirmingDiscard"
        class="flex shrink-0 items-center gap-3 border-b border-red-200 bg-red-50 px-4 py-2.5"
      >
        <span class="flex-1 text-sm text-red-800">Forkast kladden?</span>
        <button
          type="button"
          class="inline-flex items-center gap-1.5 rounded-md bg-red-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-red-700"
          @click="discardDraft"
        >
          <Trash2 class="h-3.5 w-3.5" />
          Forkast
        </button>
        <button
          type="button"
          class="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-xs font-medium text-gray-700 hover:bg-gray-50"
          @click="confirmingDiscard = false"
        >
          Hald fram
        </button>
      </div>

      <!-- Body -->
      <div class="min-h-0 flex-1 overflow-hidden p-5">
        <!-- Compose step -->
        <div v-if="step === 'compose'" class="flex h-full flex-col gap-4">
          <!-- Mode tabs: colour-coded blue (standard) / amber (broadcast)
               to match the notice below. -->
          <div class="flex gap-2 rounded-lg border border-gray-200 bg-gray-50 p-1">
            <button
              type="button"
              class="flex flex-1 items-center justify-center gap-1.5 rounded-md px-3 py-1.5 text-sm font-semibold transition"
              :class="!isBroadcast ? 'bg-blue-600 text-white shadow-sm' : 'text-gray-600 hover:bg-gray-100'"
              @click="setMode('standard')"
            >
              <Users class="h-4 w-4" />
              Standard
            </button>
            <button
              type="button"
              class="flex flex-1 items-center justify-center gap-1.5 rounded-md px-3 py-1.5 text-sm font-semibold transition"
              :class="isBroadcast ? 'bg-amber-500 text-white shadow-sm' : 'text-gray-600 hover:bg-gray-100'"
              @click="setMode('broadcast')"
            >
              <Megaphone class="h-4 w-4" />
              Utsending
            </button>
          </div>

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

          <div>
            <label class="block text-xs font-medium uppercase tracking-wide text-gray-500">Mottakarar</label>
            <div class="mt-1">
              <RecipientPicker v-model="recipients" :allow-groups="isBroadcast" />
            </div>
          </div>

          <!-- Standard notice (blue): one shared email, recipients visible. -->
          <div
            v-if="hasRecipients && !isBroadcast"
            class="flex items-start gap-2.5 rounded-md border-2 border-blue-300 bg-blue-50 px-3 py-2.5 text-sm text-blue-900"
          >
            <Users class="mt-0.5 h-5 w-5 shrink-0 text-blue-600" />
            <div>
              <p class="font-semibold">Vanleg e-post — alle mottakarane ser kvarandre</p>
              <p class="mt-0.5 text-blue-800">
                Sendt som éi melding der alle mottakarane står i To-feltet og ser kvarandre sine e-postadresser.
              </p>
            </div>
          </div>

          <!-- Broadcast notice (amber): individual, tracked sends. -->
          <div
            v-if="hasRecipients && isBroadcast"
            class="flex items-start gap-2.5 rounded-md border-2 border-amber-400 bg-amber-50 px-3 py-2.5 text-sm text-amber-900"
          >
            <Megaphone class="mt-0.5 h-5 w-5 shrink-0 text-amber-600" />
            <div>
              <p class="font-semibold">Dette blir sendt som ei utsending (broadcast)</p>
              <p class="mt-0.5 text-amber-800">
                Éin individuell e-post til kvar mottakar i: {{ recipientSummary }}. Mottakarane ser ikkje kvarandre, og leveringa kan følgjast under «Utsendingar».
              </p>
            </div>
          </div>

          <div>
            <label class="block text-xs font-medium uppercase tracking-wide text-gray-500">Emne</label>
            <input
              v-model="subject"
              type="text"
              class="mt-1 w-full rounded-md border border-gray-300 px-3 py-1.5 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <div class="flex min-h-0 flex-1 flex-col">
            <label class="block text-xs font-medium uppercase tracking-wide text-gray-500">Melding</label>
            <div class="mt-1 flex min-h-0 flex-1 flex-col gap-0">
              <RichEditor ref="editorRef" v-model="body" :address="fromAddress" class="flex-1" />
              <div
                v-if="selectedMailbox"
                class="shrink-0 cursor-default select-none rounded-b-md border border-t-0 border-dashed border-gray-300 bg-gray-50 px-3 py-2.5 text-xs text-gray-500"
              >
                <p class="mb-1 font-medium uppercase tracking-wide text-gray-400" style="font-size:10px">Signatur (automatisk)</p>
                <p>Med venleg helsing,</p>
                <p class="mt-0.5 font-medium text-gray-700">{{ selectedMailbox.from_name }}</p>
                <p class="text-gray-500">{{ selectedMailbox.address }}</p>
              </div>
            </div>
          </div>

          <div class="flex shrink-0 justify-end">
            <button
              type="button"
              :disabled="!canPreview"
              class="inline-flex items-center gap-2 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40"
              @click="goToPreview"
            >
              Førehandsvis
            </button>
          </div>
        </div>

        <!-- Preview step -->
        <div v-else class="flex h-full flex-col gap-4">
          <!-- Queued confirmation: the bulk send was accepted (202). -->
          <div v-if="queued" class="flex h-full flex-col items-center justify-center gap-4 text-center">
            <div class="rounded-full bg-green-100 p-3">
              <Send class="h-6 w-6 text-green-600" />
            </div>
            <div>
              <p class="text-base font-semibold text-gray-900">Meldinga er sett i kø</p>
              <p class="mt-1 text-sm text-gray-500">
                {{ queuedCount }} mottakarar — sendt som individuelle e-postar. Følg leveringa under «Utsendingar».
              </p>
            </div>
            <div class="flex gap-3">
              <button
                type="button"
                class="inline-flex items-center gap-2 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700"
                @click="viewBroadcasts"
              >
                Sjå utsendingar
              </button>
              <button
                type="button"
                class="rounded-md border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
                @click="$emit('close')"
              >
                Lukk
              </button>
            </div>
          </div>

          <template v-else>
            <div class="flex-1 overflow-y-auto rounded-lg border border-gray-200 bg-gray-50 p-5 text-sm">
              <div class="mb-1 text-gray-500">
                <span class="font-medium text-gray-700">Fra:</span> {{ selectedFromLabel }}
              </div>
              <div class="mb-3 text-gray-500">
                <span class="font-medium text-gray-700">Til:</span> {{ recipientSummary }}
              </div>
              <h3 class="text-base font-semibold text-gray-900">{{ subject }}</h3>
              <div class="prose prose-sm mt-3 max-w-none text-gray-700" v-html="body" />
              <div v-if="selectedMailbox" v-html="signatureHtml" class="text-sm" />
            </div>

            <!-- Send-mode notice, colour-coded to match the compose tabs. -->
            <div
              v-if="isBroadcast"
              class="shrink-0 rounded-md border-2 border-amber-400 bg-amber-50 px-3 py-2 text-sm text-amber-900"
            >
              {{ bulkNotice }}
            </div>
            <div
              v-else
              class="shrink-0 rounded-md border-2 border-blue-300 bg-blue-50 px-3 py-2 text-sm text-blue-900"
            >
              Sendt som éi melding — alle mottakarane står i To-feltet og ser kvarandre.
            </div>

            <div v-if="error" class="shrink-0 rounded border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-700">
              {{ error }}
            </div>

            <div class="flex shrink-0 justify-end gap-3">
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
          </template>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.compose-window {
  width: clamp(640px, 62vw, 1020px);
  height: clamp(520px, 82vh, 940px);
  min-width: 480px;
  min-height: 380px;
  resize: both;
  overflow: hidden;
}
</style>
