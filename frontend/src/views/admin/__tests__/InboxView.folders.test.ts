import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import InboxView from '@/views/admin/InboxView.vue'

// fetchApi is routed by URL: folders, threads, and thread detail.
const fetchApi = vi.fn(async (rawUrl: string) => {
  const url = String(rawUrl ?? '')
  if (url.includes('/folders')) {
    return {
      folders: [
        { name: 'Inbox', role: 'inbox', unread: 2, total: 5 },
        { name: 'Archive', role: 'archive', unread: 0, total: 3 },
        { name: 'Sent', role: 'sent', unread: 0, total: 1 },
      ],
    }
  }
  if (url.includes('/threads')) return { threads: [], total: 0 }
  return {}
})

vi.mock('@/composables/useApi', () => ({
  useApi: () => ({ fetchApi }),
}))

vi.mock('@/composables/useFreshTotp', () => ({
  useFreshTotp: () => ({ ensureFreshTotp: vi.fn().mockResolvedValue(true), totpAwareFetch: vi.fn() }),
}))

const mailbox = {
  address: 'kasserar@x.no',
  role: 'treasurer',
  display_name: 'Kasserar',
  unread: 2,
  total: 5,
  can_send_as: true,
}

function mountInbox() {
  return mountWithPlugins(InboxView, {
    initialRoute: '/admin?address=kasserar@x.no',
    piniaOptions: {
      initialState: { inboxUnread: { mailboxes: [mailbox], accessible: true } },
    },
    global: { stubs: { ComposeModal: true, RichEditor: true, BroadcastHistory: true } },
  })
}

describe('InboxView folder navigation (BRY-190)', () => {
  beforeEach(() => fetchApi.mockClear())

  it('renders the selected mailbox folder subrows on mount', async () => {
    const wrapper = mountInbox()
    await flushPromises()
    await flushPromises()

    expect(fetchApi).toHaveBeenCalledWith(
      expect.stringContaining('/inbox/kasserar%40x.no/folders'),
    )
    // Folder labels fall back to the JMAP folder name (i18n is mocked).
    expect(wrapper.text()).toContain('Archive')
    expect(wrapper.text()).toContain('Sent')
  })

  it('requests folder-scoped threads when a non-Inbox folder is selected', async () => {
    const wrapper = mountInbox()
    await flushPromises()
    await flushPromises()

    const archiveBtn = wrapper
      .findAll('button')
      .find((b) => b.text().includes('Archive'))
    expect(archiveBtn).toBeTruthy()
    await archiveBtn!.trigger('click')
    await flushPromises()
    await flushPromises()

    const calledFolderScoped = fetchApi.mock.calls.some(
      ([url]) => url.includes('/threads') && url.includes('folder=archive'),
    )
    expect(calledFolderScoped).toBe(true)
  })
})
