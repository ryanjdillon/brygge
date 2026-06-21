<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Inbox, Mail, Archive, Check, ImageOff, Image as ImageIcon, AlertTriangle, Reply, Send, X, SquarePen, ChevronRight, ChevronDown, Paperclip } from 'lucide-vue-next'
import { useApi } from '@/composables/useApi'
import { useFreshTotp } from '@/composables/useFreshTotp'
import { sanitizeEmail } from '@/lib/sanitizeHtml'
import { useInboxUnreadStore } from '@/stores/inboxUnread'
import { storeToRefs } from 'pinia'
import { formatDateTime as fmtDateTime } from '@/lib/format'
import ComposeModal from '@/components/admin/ComposeModal.vue'
import RichEditor from '@/components/ui/RichEditor.vue'
import Tabs from '@/components/ui/Tabs.vue'
import BroadcastHistory from '@/components/admin/BroadcastHistory.vue'

interface MailboxView {
  address: string
  role: string
  display_name: string
  unread: number
  total: number
  can_send_as: boolean
}

interface Folder {
  name: string
  role: string
  unread: number
  total: number
}

interface ThreadRow {
  thread_id: string
  subject: string
  from: { name: string; email: string }[]
  preview: string
  received_at: string
  unread: boolean
  has_attachment: boolean
}

interface EmailFull {
  id: string
  threadId: string
  subject: string
  from: { name: string; email: string }[]
  to: { name: string; email: string }[]
  preview: string
  receivedAt: string
  hasAttachment: boolean
  messageId?: string[]
  htmlBody: { partId: string; type: string }[]
  textBody: { partId: string; type: string }[]
  bodyValues: Record<string, { value: string }>
  attachments: { blobId: string; name: string; size: number; type: string }[]
}

function htmlToText(html: string): string {
  const el = document.createElement('div')
  el.innerHTML = html
  return el.innerText
}

const { t, te, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const { fetchApi } = useApi()

const inboxUnread = useInboxUnreadStore()
const { mailboxes: storeMailboxes, totalUnread: storeTotalUnread } = storeToRefs(inboxUnread)
const mailboxes = computed(() => storeMailboxes.value as unknown as MailboxView[])

const threads = ref<ThreadRow[]>([])
// mailboxes is bound to the Pinia store above; this comment marks
// the seam so future edits don't accidentally reintroduce a local
// ref and break NavBar-vs-InboxView sync.
const thread = ref<{ thread_id: string; emails: EmailFull[] } | null>(null)
// Initial-load spinner only — the 30s background poll doesn't flip
// this, so the sidebar list doesn't flash empty between refreshes.
const loadingMailboxes = ref(false)
const loadingThreads = ref(false)
const loadingThread = ref(false)
const error = ref<string | null>(null)
const sendError = ref<string | null>(null) // composer-scoped error, separate from page-level
const sendSuccess = ref(false)
const showImages = ref(false)
const search = ref('')
const showCompose = ref(false)
const { ensureFreshTotp } = useFreshTotp()

// Inbox vs sent-broadcasts history. Local state — the three-pane inbox
// keeps its own ?address/?thread URL sync; the tab just toggles which
// surface is shown.
const activeTab = ref<'inbox' | 'broadcasts'>('inbox')
const inboxTabs = computed(() => [
  { value: 'inbox', label: t('admin.inbox.tabInbox') },
  { value: 'broadcasts', label: t('admin.inbox.tabBroadcasts') },
])

async function openCompose() {
  if (!await ensureFreshTotp()) return
  showCompose.value = true
}

const selectedAddress = computed(() => (route.query.address as string) || mailboxes.value[0]?.address || '')
const selectedThread = computed(() => (route.query.thread as string) || '')
// Empty string means the Inbox folder (the default); any other value is
// a folder role or name passed straight to the threads endpoint.
const selectedFolder = computed(() => (route.query.folder as string) || '')

// Folder navigation (BRY-190). Each shared mailbox expands to show the
// JMAP folders that actually exist for it; selecting one lists that
// folder's threads. Folders are fetched lazily per mailbox and cached.
const expanded = ref<Record<string, boolean>>({})
const foldersByAddress = ref<Record<string, Folder[]>>({})

async function loadFolders(addr: string, force = false) {
  if (!addr || (foldersByAddress.value[addr] && !force)) return
  try {
    const res = await fetchApi<{ folders: Folder[] }>(
      `/api/v1/admin/inbox/${encodeURIComponent(addr)}/folders`,
    )
    foldersByAddress.value = { ...foldersByAddress.value, [addr]: res.folders ?? [] }
  } catch (e) {
    console.error('[inbox] loadFolders', e)
  }
}

function toggleExpand(addr: string) {
  const next = !expanded.value[addr]
  expanded.value = { ...expanded.value, [addr]: next }
  if (next) loadFolders(addr)
}

// A folder's URL selector: Inbox carries no param; folders with a role
// use the role; custom folders fall back to their name.
function folderSelector(f: Folder): string {
  if (f.role && f.role.toLowerCase() === 'inbox') return ''
  return f.role || f.name
}

function folderLabel(f: Folder): string {
  const key = `admin.inbox.folder.${f.role}`
  return f.role && te(key) ? t(key) : f.name
}

function isFolderActive(addr: string, f: Folder): boolean {
  return addr === selectedAddress.value && selectedFolder.value === folderSelector(f)
}

function selectFolder(addr: string, sel: string) {
  router.replace({ query: { ...route.query, address: addr, folder: sel || undefined, thread: undefined } })
}

const totalUnread = computed(() => storeTotalUnread.value)

// reportError stores a friendly i18n message in `error.value` and
// logs the raw API error to the console for debugging. Keeps user-
// visible text out of API-string territory.
function reportError(key: string, e: unknown) {
  console.error('[inbox]', key, e)
  error.value = t(key)
}

async function loadMailboxes(opts: { background?: boolean } = {}) {
  if (!opts.background) loadingMailboxes.value = true
  try {
    await inboxUnread.refresh({ silent: !!opts.background })
    if (!selectedAddress.value && mailboxes.value[0]) {
      selectAddress(mailboxes.value[0].address)
    }
  } catch (e) {
    if (!opts.background) reportError('admin.inbox.error.loadMailboxes', e)
  } finally {
    if (!opts.background) loadingMailboxes.value = false
  }
}

async function loadThreads() {
  if (!selectedAddress.value) {
    threads.value = []
    return
  }
  loadingThreads.value = true
  error.value = null
  try {
    const q = new URLSearchParams()
    if (search.value.trim()) q.set('q', search.value.trim())
    if (selectedFolder.value) q.set('folder', selectedFolder.value)
    const url = `/api/v1/admin/inbox/${encodeURIComponent(selectedAddress.value)}/threads${q.size ? '?' + q : ''}`
    const res = await fetchApi<{ threads: ThreadRow[]; total: number }>(url)
    threads.value = res.threads ?? []
  } catch (e) {
    reportError('admin.inbox.error.loadThreads', e)
    threads.value = []
  } finally {
    loadingThreads.value = false
  }
}

async function loadThread() {
  if (!selectedAddress.value || !selectedThread.value) {
    thread.value = null
    return
  }
  loadingThread.value = true
  try {
    const url = `/api/v1/admin/inbox/${encodeURIComponent(selectedAddress.value)}/threads/${encodeURIComponent(selectedThread.value)}`
    const res = await fetchApi<{ thread_id: string; emails: EmailFull[] }>(url)
    thread.value = res
  } catch (e) {
    reportError('admin.inbox.error.loadThread', e)
    thread.value = null
  } finally {
    loadingThread.value = false
  }
}

function selectAddress(addr: string) {
  // Selecting a mailbox shows its Inbox: clear any folder/thread, expand
  // the row, and load its folder list.
  router.replace({ query: { ...route.query, address: addr, folder: undefined, thread: undefined } })
  expanded.value = { ...expanded.value, [addr]: true }
  loadFolders(addr)
}

function selectThread(id: string) {
  const row = threads.value.find((t) => t.thread_id === id)
  const wasUnread = row?.unread ?? false
  router.replace({ query: { ...route.query, thread: id } })
  // Opening a thread marks it read. The server previously only audited
  // the view and never set $seen, so reads never stuck — this persists it.
  if (wasUnread) markRead(true, id)
}

// Read state of the currently open thread — drives the toggle button.
const currentThreadUnread = computed(
  () => threads.value.find((t) => t.thread_id === selectedThread.value)?.unread ?? false,
)

async function markRead(read: boolean, threadId: string = selectedThread.value) {
  if (!selectedAddress.value || !threadId) return
  // Optimistic: flip the row + refresh the badge store, without a full
  // list reload (which would reorder/flicker the list). The NavBar
  // InboxIndicator subscribes to the same store, so the badge updates
  // in lockstep without waiting for its poll.
  const row = threads.value.find((t) => t.thread_id === threadId)
  const prev = row?.unread
  if (row) row.unread = !read
  const url = `/api/v1/admin/inbox/${encodeURIComponent(selectedAddress.value)}/threads/${encodeURIComponent(threadId)}/mark_read?read=${read}`
  try {
    await fetchApi(url, { method: 'POST' })
    await inboxUnread.refresh({ silent: true })
  } catch (e) {
    if (row && prev !== undefined) row.unread = prev
    reportError('admin.inbox.error.markRead', e)
  }
}

// --- Reply composer (DIL-278) ---------------------------------------------

const composing = ref(false)
const replyTo = ref('')
const replyCc = ref('')
const replySubject = ref('')
const replyBody = ref('')
const sending = ref(false)
const replyEditorRef = ref<InstanceType<typeof RichEditor> | null>(null)

function openReply() {
  if (!thread.value || thread.value.emails.length === 0) return
  const latest = thread.value.emails[thread.value.emails.length - 1]
  // Default recipient: whoever sent the latest message.
  replyTo.value = (latest.from?.[0]?.email) ?? ''
  replyCc.value = ''
  const subj = latest.subject || ''
  replySubject.value = /^re:\s/i.test(subj) ? subj : (subj ? `Re: ${subj}` : '')
  replyBody.value = ''
  composing.value = true
}

function cancelReply() {
  composing.value = false
}

function parseAddresses(raw: string): { email: string }[] {
  return raw
    .split(/[,;\n]/)
    .map((s) => s.trim())
    .filter(Boolean)
    .map((email) => ({ email }))
}

async function submitReply() {
  if (!selectedAddress.value || !thread.value || sending.value) return
  const to = parseAddresses(replyTo.value)
  if (to.length === 0) {
    sendError.value = t('admin.inbox.error.toRequired')
    return
  }
  sending.value = true
  sendError.value = null
  sendSuccess.value = false
  try {
    const latest = thread.value.emails[thread.value.emails.length - 1]
    const inReplyTo = latest.messageId?.[0] ?? ''
    const payload = {
      to,
      cc: parseAddresses(replyCc.value),
      subject: replySubject.value,
      body_html: replyBody.value,
      body_text: htmlToText(replyBody.value),
      in_reply_to: inReplyTo,
      attachments: replyEditorRef.value?.attachments ?? [],
    }
    const url = `/api/v1/admin/inbox/${encodeURIComponent(selectedAddress.value)}/send`
    console.log('[inbox] submitting reply', { url, to: to.length, subject: payload.subject })
    const res = await fetchApi<{ email_id: string; message_id: string }>(url, {
      method: 'POST',
      body: JSON.stringify(payload),
    })
    console.log('[inbox] reply sent', res)
    sendSuccess.value = true
    // Keep the composer visible briefly so the user sees the
    // success indicator; then collapse and refresh.
    setTimeout(() => {
      composing.value = false
      sendSuccess.value = false
    }, 1200)
    await loadThreads()
    await loadThread()
  } catch (e) {
    console.error('[inbox] reply failed', e)
    sendError.value = (e as Error)?.message || t('admin.inbox.error.send')
  } finally {
    sending.value = false
  }
}

async function archiveCurrent() {
  if (!selectedAddress.value || !selectedThread.value) return
  const url = `/api/v1/admin/inbox/${encodeURIComponent(selectedAddress.value)}/threads/${encodeURIComponent(selectedThread.value)}/archive`
  try {
    await fetchApi(url, { method: 'POST' })
    router.replace({ query: { ...route.query, thread: undefined } })
    await Promise.all([loadThreads(), inboxUnread.refresh({ silent: true })])
    // Archiving moves the thread between folders — refresh per-folder counts.
    loadFolders(selectedAddress.value, true)
  } catch (e) {
    reportError('admin.inbox.error.archive', e)
  }
}

function renderBody(email: EmailFull): string {
  // Prefer first HTML part; fall back to text rendered as <pre>.
  if (email.htmlBody?.length) {
    const part = email.htmlBody[0]
    const raw = email.bodyValues?.[part.partId]?.value ?? ''
    return sanitizeEmail(raw, {
      showImages: showImages.value,
      proxyBase: '/api/v1/admin/inbox/proxy-image',
    })
  }
  if (email.textBody?.length) {
    const part = email.textBody[0]
    const raw = email.bodyValues?.[part.partId]?.value ?? ''
    const escaped = raw
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
    return `<pre style="white-space:pre-wrap;font-family:inherit">${escaped}</pre>`
  }
  return ''
}

function formatFrom(addrs: { name: string; email: string }[]): string {
  if (!addrs?.length) return ''
  return addrs.map((a) => a.name || a.email).join(', ')
}

function formatDate(iso: string): string {
  return fmtDateTime(iso, locale.value)
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}

// Reactive refetch on URL changes.
watch(() => route.query.address, (addr) => {
  thread.value = null
  if (addr) { expanded.value = { ...expanded.value, [addr as string]: true }; loadFolders(addr as string) }
  loadThreads()
})
watch(() => route.query.folder, () => { thread.value = null; loadThreads() })
watch(() => route.query.thread, () => loadThread())
watch(search, () => loadThreads())

// 30 s mailbox-count poll for the unread badge.
let pollTimer: ReturnType<typeof setInterval> | null = null
onMounted(async () => {
  await loadMailboxes()
  if (selectedAddress.value) {
    expanded.value = { ...expanded.value, [selectedAddress.value]: true }
    loadFolders(selectedAddress.value)
  }
  await loadThreads()
  await loadThread()
  // Background poll — pass {background:true} so the loading state
  // doesn't flip and the sidebar doesn't flicker.
  pollTimer = setInterval(() => loadMailboxes({ background: true }), 30_000)
})
onUnmounted(() => { if (pollTimer) clearInterval(pollTimer) })
</script>

<template>
  <div class="flex h-[calc(100vh-4rem)] flex-col bg-gray-50">
    <header class="flex items-center justify-between border-b border-gray-200 bg-white px-4 py-3">
      <h1 class="flex items-center gap-2 text-lg font-semibold">
        <Inbox class="h-5 w-5 text-gray-500" />
        {{ t('admin.sidebar.inbox') }}
        <span v-if="totalUnread" class="ml-2 rounded-full bg-blue-100 px-2 py-0.5 text-sm font-medium text-blue-700">
          {{ totalUnread }}
        </span>
      </h1>
      <div v-show="activeTab === 'inbox'" class="flex items-center gap-3">
        <button
          type="button"
          class="inline-flex items-center gap-1.5 rounded-md bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-700"
          @click="openCompose"
        >
          <SquarePen class="h-4 w-4" />
          Ny melding
        </button>
        <input
          v-model="search"
          type="search"
          :placeholder="t('admin.inbox.searchPlaceholder')"
          class="w-64 rounded border border-gray-300 px-3 py-1.5 text-sm focus:border-blue-500 focus:outline-none"
        />
      </div>
    </header>

    <div class="border-b border-gray-200 bg-white px-4">
      <Tabs v-model="activeTab" :tabs="inboxTabs" />
    </div>

    <ComposeModal
      v-if="showCompose"
      :mailboxes="(mailboxes as any)"
      @close="showCompose = false"
      @view-broadcasts="showCompose = false; activeTab = 'broadcasts'"
    />

    <div v-show="activeTab === 'inbox'" class="flex items-center gap-2 border-b border-blue-100 bg-blue-50 px-4 py-2 text-sm text-blue-800">
      <Mail class="h-4 w-4 shrink-0 text-blue-500" />
      <span>{{ t('admin.inbox.clientNotice') }}</span>
      <a
        href="https://github.com/ryanjdillon/brygge/blob/main/docs/user/setting-up-email.md"
        target="_blank"
        rel="noopener noreferrer"
        class="ml-1 font-medium underline hover:text-blue-900"
      >{{ t('admin.inbox.clientNoticeLink') }}</a>
    </div>

    <div v-if="error" class="border-b border-red-200 bg-red-50 px-4 py-2 text-sm text-red-700">
      <AlertTriangle class="mr-1 inline h-4 w-4" />
      {{ error }}
    </div>

    <BroadcastHistory v-if="activeTab === 'broadcasts'" />

    <div v-show="activeTab === 'inbox'" class="flex min-h-0 flex-1">
      <!-- Pane 1: mailbox list -->
      <aside class="w-56 shrink-0 border-r border-gray-200 bg-white">
        <div v-if="loadingMailboxes" class="p-4 text-sm text-gray-500">{{ t('common.loading') }}</div>
        <ul v-else-if="mailboxes.length" class="divide-y divide-gray-100">
          <li v-for="m in mailboxes" :key="m.address">
            <div
              class="flex items-center text-sm hover:bg-gray-50"
              :class="{ 'bg-blue-50 font-semibold text-blue-900': m.address === selectedAddress && !selectedFolder }"
            >
              <button
                type="button"
                class="flex shrink-0 items-center justify-center py-2 pl-2 pr-1 text-gray-400 hover:text-gray-600"
                :aria-label="expanded[m.address] ? t('admin.inbox.folders.collapse') : t('admin.inbox.folders.expand')"
                @click="toggleExpand(m.address)"
              >
                <component :is="expanded[m.address] ? ChevronDown : ChevronRight" class="h-4 w-4" />
              </button>
              <button
                type="button"
                class="flex flex-1 items-center justify-between py-2 pr-3 text-left"
                @click="selectAddress(m.address)"
              >
                <span class="truncate">{{ m.display_name }}</span>
                <span v-if="m.unread" class="ml-2 rounded bg-blue-600 px-1.5 py-0.5 text-xs font-medium text-white">
                  {{ m.unread }}
                </span>
              </button>
            </div>
            <!-- Folder subrows (BRY-190): the JMAP folders that exist for
                 this mailbox. Selecting one lists that folder's threads. -->
            <ul v-if="expanded[m.address] && foldersByAddress[m.address]?.length" class="bg-gray-50/50">
              <li v-for="f in foldersByAddress[m.address]" :key="f.role + ':' + f.name">
                <button
                  type="button"
                  class="flex w-full items-center justify-between py-1.5 pl-9 pr-3 text-left text-sm hover:bg-gray-100"
                  :class="isFolderActive(m.address, f)
                    ? 'bg-blue-50 font-medium text-blue-900'
                    : 'text-gray-600'"
                  @click="selectFolder(m.address, folderSelector(f))"
                >
                  <span class="truncate">{{ folderLabel(f) }}</span>
                  <span v-if="f.unread" class="ml-2 rounded bg-blue-500 px-1.5 py-0.5 text-xs font-medium text-white">
                    {{ f.unread }}
                  </span>
                </button>
              </li>
            </ul>
          </li>
        </ul>
        <div v-else class="p-4 text-sm text-gray-500">{{ t('admin.inbox.empty.mailboxes') }}</div>
      </aside>

      <!-- Pane 2: thread list -->
      <section class="w-96 shrink-0 overflow-y-auto border-r border-gray-200 bg-white">
        <div v-if="loadingThreads" class="p-4 text-sm text-gray-500">{{ t('common.loading') }}</div>
        <ul v-else-if="threads.length" class="divide-y-2 divide-gray-200">
          <li v-for="row in threads" :key="row.thread_id">
            <button
              type="button"
              class="flex w-full flex-col gap-1 px-3 py-2 text-left hover:bg-gray-50"
              :class="{
                'bg-blue-50': row.thread_id === selectedThread,
                'font-semibold text-gray-900': row.unread,
                'text-gray-700': !row.unread,
              }"
              @click="selectThread(row.thread_id)"
            >
              <div class="flex items-center justify-between text-sm">
                <span class="truncate">{{ formatFrom(row.from) }}</span>
                <span class="ml-2 shrink-0 text-xs text-gray-500">{{ formatDate(row.received_at) }}</span>
              </div>
              <div class="truncate text-sm">{{ row.subject || t('admin.inbox.noSubject') }}</div>
              <div class="truncate text-xs text-gray-500">{{ row.preview }}</div>
            </button>
          </li>
        </ul>
        <div v-else class="p-4 text-sm text-gray-500">{{ t('admin.inbox.empty.threads') }}</div>
      </section>

      <!-- Pane 3: reader -->
      <main class="flex-1 overflow-y-auto bg-gray-200">
        <div v-if="loadingThread" class="p-6 text-sm text-gray-500">{{ t('common.loading') }}</div>
        <div v-else-if="!thread" class="flex h-full items-center justify-center text-sm text-gray-400">
          <Mail class="mr-2 h-5 w-5" />
          {{ t('admin.inbox.empty.reader') }}
        </div>
        <article v-else>
          <header class="sticky top-0 z-10 flex items-center justify-between border-b border-gray-200 bg-white px-6 py-3">
            <h2 class="text-base font-semibold">{{ thread.emails[0]?.subject || t('admin.inbox.noSubject') }}</h2>
            <div class="flex items-center gap-2">
              <button
                type="button"
                class="inline-flex items-center gap-1 rounded border border-blue-300 bg-blue-50 px-2 py-1 text-xs text-blue-700 hover:bg-blue-100"
                @click="openReply"
              >
                <Reply class="h-4 w-4" /> {{ t('admin.inbox.reply.button') }}
              </button>
              <button
                type="button"
                class="inline-flex items-center gap-1 rounded border border-gray-300 px-2 py-1 text-xs hover:bg-gray-50"
                @click="showImages = !showImages"
              >
                <component :is="showImages ? ImageIcon : ImageOff" class="h-4 w-4" />
                {{ showImages ? t('admin.inbox.imagesOn') : t('admin.inbox.imagesOff') }}
              </button>
              <button
                type="button"
                class="inline-flex items-center gap-1 rounded border border-gray-300 px-2 py-1 text-xs hover:bg-gray-50"
                @click="markRead(currentThreadUnread)"
              >
                <component :is="currentThreadUnread ? Check : Mail" class="h-4 w-4" />
                {{ currentThreadUnread ? t('admin.inbox.markAsRead') : t('admin.inbox.markAsUnread') }}
              </button>
              <button
                type="button"
                class="inline-flex items-center gap-1 rounded border border-gray-300 px-2 py-1 text-xs hover:bg-gray-50"
                @click="archiveCurrent"
              >
                <Archive class="h-4 w-4" /> {{ t('admin.inbox.archive') }}
              </button>
            </div>
          </header>
          <!-- Reply composer (DIL-278). Inline panel; submitting
               sends as the shared principal with X-Brygge-Actor
               header set to the logged-in user. POST is gated by
               RequireFreshTOTP on the backend. -->
          <section v-if="composing" class="border-b border-gray-200 bg-gray-50 px-6 py-4">
            <header class="mb-2 flex items-center justify-between">
              <h3 class="text-sm font-semibold">{{ t('admin.inbox.reply.heading') }}</h3>
              <button type="button" class="text-gray-500 hover:text-gray-700" @click="cancelReply" :aria-label="t('admin.inbox.reply.cancel')">
                <X class="h-4 w-4" />
              </button>
            </header>
            <div class="space-y-2 text-sm">
              <label class="flex items-center gap-2">
                <span class="w-16 text-xs text-gray-600">{{ t('admin.inbox.reply.to') }}</span>
                <input v-model="replyTo" type="text" class="flex-1 rounded border border-gray-300 px-2 py-1" />
              </label>
              <label class="flex items-center gap-2">
                <span class="w-16 text-xs text-gray-600">{{ t('admin.inbox.reply.cc') }}</span>
                <input v-model="replyCc" type="text" class="flex-1 rounded border border-gray-300 px-2 py-1" />
              </label>
              <label class="flex items-center gap-2">
                <span class="w-16 text-xs text-gray-600">{{ t('admin.inbox.reply.subject') }}</span>
                <input v-model="replySubject" type="text" class="flex-1 rounded border border-gray-300 px-2 py-1" />
              </label>
              <RichEditor v-model="replyBody" :address="selectedAddress" ref="replyEditorRef" />
              <div
                v-if="sendError"
                class="rounded border border-red-300 bg-red-50 px-2 py-1 text-xs text-red-700"
              >
                {{ sendError }}
              </div>
              <div
                v-else-if="sendSuccess"
                class="rounded border border-green-300 bg-green-50 px-2 py-1 text-xs text-green-700"
              >
                {{ t('admin.inbox.reply.sent') }}
              </div>
              <div class="flex justify-end gap-2">
                <button
                  type="button"
                  class="rounded border border-gray-300 px-3 py-1 text-xs hover:bg-gray-100"
                  @click="cancelReply"
                  :disabled="sending"
                >
                  {{ t('admin.inbox.reply.cancel') }}
                </button>
                <button
                  type="button"
                  class="inline-flex items-center gap-1 rounded bg-blue-600 px-3 py-1 text-xs text-white hover:bg-blue-700 disabled:opacity-50"
                  :disabled="sending"
                  @click="submitReply"
                >
                  <Send class="h-4 w-4" />
                  {{ sending ? t('admin.inbox.reply.sending') : t('admin.inbox.reply.send') }}
                </button>
              </div>
            </div>
          </section>

          <div class="space-y-4 px-6 py-4">
            <section v-for="email in thread.emails" :key="email.id" class="rounded border border-gray-200 bg-white p-4">
              <header class="mb-2 flex items-center justify-between text-sm text-gray-600">
                <div>
                  <span class="font-medium text-gray-900">{{ formatFrom(email.from) }}</span>
                  <span class="ml-2 text-xs">→ {{ formatFrom(email.to) }}</span>
                </div>
                <span class="text-xs">{{ formatDate(email.receivedAt) }}</span>
              </header>
              <div class="prose prose-sm max-w-none break-words" v-html="renderBody(email)" />
              <footer v-if="email.attachments?.length" class="mt-3 flex flex-wrap gap-2 border-t border-gray-100 pt-2 text-xs text-gray-500">
                <a
                  v-for="a in email.attachments"
                  :key="a.blobId"
                  :href="`/api/v1/admin/inbox/${encodeURIComponent(selectedAddress)}/blob/${encodeURIComponent(a.blobId)}?name=${encodeURIComponent(a.name || 'attachment')}`"
                  :download="a.name || 'attachment'"
                  class="inline-flex items-center gap-1 rounded bg-gray-100 px-2 py-1 text-blue-700 hover:bg-gray-200"
                >
                  <Paperclip class="h-3 w-3" />
                  {{ a.name || a.type }}
                  <span v-if="a.size" class="text-gray-400">({{ formatBytes(a.size) }})</span>
                </a>
              </footer>
            </section>
          </div>
        </article>
      </main>
    </div>
  </div>
</template>
