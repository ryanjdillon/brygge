<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Inbox, Mail, Archive, Check, ImageOff, Image as ImageIcon, AlertTriangle } from 'lucide-vue-next'
import { useApi } from '@/composables/useApi'
import { sanitizeEmail } from '@/lib/sanitizeHtml'

interface MailboxView {
  address: string
  role: string
  display_name: string
  unread: number
  total: number
  can_send_as: boolean
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
  htmlBody: { partId: string; type: string }[]
  textBody: { partId: string; type: string }[]
  bodyValues: Record<string, { value: string }>
  attachments: { blobId: string; name: string; size: number; type: string }[]
}

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const { fetchApi } = useApi()

const mailboxes = ref<MailboxView[]>([])
const threads = ref<ThreadRow[]>([])
const thread = ref<{ thread_id: string; emails: EmailFull[] } | null>(null)
const loadingMailboxes = ref(false)
const loadingThreads = ref(false)
const loadingThread = ref(false)
const error = ref<string | null>(null)
const showImages = ref(false)
const search = ref('')

const selectedAddress = computed(() => (route.query.address as string) || mailboxes.value[0]?.address || '')
const selectedThread = computed(() => (route.query.thread as string) || '')

const totalUnread = computed(() => mailboxes.value.reduce((s, m) => s + m.unread, 0))

async function loadMailboxes() {
  loadingMailboxes.value = true
  try {
    const res = await fetchApi<{ mailboxes: MailboxView[] }>('/api/v1/admin/inbox/mailboxes')
    mailboxes.value = res.mailboxes ?? []
    if (!selectedAddress.value && mailboxes.value[0]) {
      selectAddress(mailboxes.value[0].address)
    }
  } catch (e: unknown) {
    error.value = (e as Error).message
  } finally {
    loadingMailboxes.value = false
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
    const url = `/api/v1/admin/inbox/${encodeURIComponent(selectedAddress.value)}/threads${q.size ? '?' + q : ''}`
    const res = await fetchApi<{ threads: ThreadRow[]; total: number }>(url)
    threads.value = res.threads ?? []
  } catch (e: unknown) {
    error.value = (e as Error).message
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
  } catch (e: unknown) {
    error.value = (e as Error).message
    thread.value = null
  } finally {
    loadingThread.value = false
  }
}

function selectAddress(addr: string) {
  router.replace({ query: { ...route.query, address: addr, thread: undefined } })
}

function selectThread(id: string) {
  router.replace({ query: { ...route.query, thread: id } })
  // Optimistically mark this row read so the next list refresh isn't required.
  const row = threads.value.find((t) => t.thread_id === id)
  if (row) row.unread = false
}

async function markRead(read: boolean) {
  if (!selectedAddress.value || !selectedThread.value) return
  const url = `/api/v1/admin/inbox/${encodeURIComponent(selectedAddress.value)}/threads/${encodeURIComponent(selectedThread.value)}/mark_read?read=${read}`
  await fetchApi(url, { method: 'POST' })
  await loadThreads()
}

async function archiveCurrent() {
  if (!selectedAddress.value || !selectedThread.value) return
  const url = `/api/v1/admin/inbox/${encodeURIComponent(selectedAddress.value)}/threads/${encodeURIComponent(selectedThread.value)}/archive`
  await fetchApi(url, { method: 'POST' })
  router.replace({ query: { ...route.query, thread: undefined } })
  await loadThreads()
  await loadMailboxes()
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
  if (!iso) return ''
  const d = new Date(iso)
  return d.toLocaleString()
}

// Reactive refetch on URL changes.
watch(() => route.query.address, () => { thread.value = null; loadThreads() })
watch(() => route.query.thread, () => loadThread())
watch(search, () => loadThreads())

// 30 s mailbox-count poll for the unread badge.
let pollTimer: ReturnType<typeof setInterval> | null = null
onMounted(async () => {
  await loadMailboxes()
  await loadThreads()
  await loadThread()
  pollTimer = setInterval(loadMailboxes, 30_000)
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
      <input
        v-model="search"
        type="search"
        :placeholder="t('inbox.searchPlaceholder')"
        class="w-64 rounded border border-gray-300 px-3 py-1.5 text-sm focus:border-blue-500 focus:outline-none"
      />
    </header>

    <div v-if="error" class="border-b border-red-200 bg-red-50 px-4 py-2 text-sm text-red-700">
      <AlertTriangle class="mr-1 inline h-4 w-4" />
      {{ error }}
    </div>

    <div class="flex min-h-0 flex-1">
      <!-- Pane 1: mailbox list -->
      <aside class="w-56 shrink-0 border-r border-gray-200 bg-white">
        <div v-if="loadingMailboxes" class="p-4 text-sm text-gray-500">{{ t('common.loading') }}</div>
        <ul v-else-if="mailboxes.length" class="divide-y divide-gray-100">
          <li v-for="m in mailboxes" :key="m.address">
            <button
              type="button"
              class="flex w-full items-center justify-between px-3 py-2 text-left text-sm hover:bg-gray-50"
              :class="{ 'bg-blue-50 font-semibold text-blue-900': m.address === selectedAddress }"
              @click="selectAddress(m.address)"
            >
              <span class="truncate">{{ m.display_name }}</span>
              <span v-if="m.unread" class="ml-2 rounded bg-blue-600 px-1.5 py-0.5 text-xs font-medium text-white">
                {{ m.unread }}
              </span>
            </button>
          </li>
        </ul>
        <div v-else class="p-4 text-sm text-gray-500">{{ t('inbox.empty.mailboxes') }}</div>
      </aside>

      <!-- Pane 2: thread list -->
      <section class="w-96 shrink-0 overflow-y-auto border-r border-gray-200 bg-white">
        <div v-if="loadingThreads" class="p-4 text-sm text-gray-500">{{ t('common.loading') }}</div>
        <ul v-else-if="threads.length" class="divide-y divide-gray-100">
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
              <div class="truncate text-sm">{{ row.subject || t('inbox.noSubject') }}</div>
              <div class="truncate text-xs text-gray-500">{{ row.preview }}</div>
            </button>
          </li>
        </ul>
        <div v-else class="p-4 text-sm text-gray-500">{{ t('inbox.empty.threads') }}</div>
      </section>

      <!-- Pane 3: reader -->
      <main class="flex-1 overflow-y-auto bg-white">
        <div v-if="loadingThread" class="p-6 text-sm text-gray-500">{{ t('common.loading') }}</div>
        <div v-else-if="!thread" class="flex h-full items-center justify-center text-sm text-gray-400">
          <Mail class="mr-2 h-5 w-5" />
          {{ t('inbox.empty.reader') }}
        </div>
        <article v-else>
          <header class="sticky top-0 z-10 flex items-center justify-between border-b border-gray-200 bg-white px-6 py-3">
            <h2 class="text-base font-semibold">{{ thread.emails[0]?.subject || t('inbox.noSubject') }}</h2>
            <div class="flex items-center gap-2">
              <button
                type="button"
                class="inline-flex items-center gap-1 rounded border border-gray-300 px-2 py-1 text-xs hover:bg-gray-50"
                @click="showImages = !showImages"
              >
                <component :is="showImages ? ImageIcon : ImageOff" class="h-4 w-4" />
                {{ showImages ? t('inbox.imagesOn') : t('inbox.imagesOff') }}
              </button>
              <button
                type="button"
                class="inline-flex items-center gap-1 rounded border border-gray-300 px-2 py-1 text-xs hover:bg-gray-50"
                @click="markRead(false)"
              >
                <Check class="h-4 w-4" /> {{ t('inbox.markUnread') }}
              </button>
              <button
                type="button"
                class="inline-flex items-center gap-1 rounded border border-gray-300 px-2 py-1 text-xs hover:bg-gray-50"
                @click="archiveCurrent"
              >
                <Archive class="h-4 w-4" /> {{ t('inbox.archive') }}
              </button>
            </div>
          </header>
          <div class="space-y-4 px-6 py-4">
            <section v-for="email in thread.emails" :key="email.id" class="rounded border border-gray-200 p-4">
              <header class="mb-2 flex items-center justify-between text-sm text-gray-600">
                <div>
                  <span class="font-medium text-gray-900">{{ formatFrom(email.from) }}</span>
                  <span class="ml-2 text-xs">→ {{ formatFrom(email.to) }}</span>
                </div>
                <span class="text-xs">{{ formatDate(email.receivedAt) }}</span>
              </header>
              <div class="prose prose-sm max-w-none break-words" v-html="renderBody(email)" />
              <footer v-if="email.attachments?.length" class="mt-3 flex flex-wrap gap-2 border-t border-gray-100 pt-2 text-xs text-gray-500">
                <span v-for="a in email.attachments" :key="a.blobId" class="rounded bg-gray-100 px-2 py-1">
                  {{ a.name || a.type }}
                </span>
              </footer>
            </section>
          </div>
        </article>
      </main>
    </div>
  </div>
</template>
