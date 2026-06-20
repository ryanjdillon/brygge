import { describe, it, expect, vi, beforeEach } from 'vitest'
import { nextTick } from 'vue'
import { mountWithPlugins } from '@/test/test-utils'
import ComposeModal from '@/components/admin/ComposeModal.vue'

const fetchApi = vi.fn()
vi.mock('@/composables/useApi', () => ({
  useApi: () => ({ fetchApi }),
}))

const mailboxes = [{ address: 'styret@x.no', display_name: 'Styret', can_send_as: true }]

function mountModal() {
  return mountWithPlugins(ComposeModal, {
    props: { mailboxes },
    global: { stubs: { RecipientPicker: true, RichEditor: true, teleport: true } },
  })
}

describe('ComposeModal bulk UX', () => {
  beforeEach(() => fetchApi.mockReset())

  it('shows the individual-send notice in the preview for a group send', async () => {
    const wrapper = mountModal()
    const vm = wrapper.vm as unknown as {
      recipients: { groups: string[]; individuals: unknown[] }
      subject: string
      body: string
      step: string
    }
    vm.recipients = { groups: ['members'], individuals: [] }
    vm.subject = 'Hei'
    vm.body = 'Body'
    vm.step = 'preview'
    await nextTick()

    expect(wrapper.text()).toContain('individuelle e-postar')
  })

  it('renders the queued state and emits view-broadcasts after a 202 response', async () => {
    fetchApi.mockResolvedValue({ broadcast_id: 'b1', recipient_count: 5 })
    const wrapper = mountModal()
    const vm = wrapper.vm as unknown as {
      recipients: { groups: string[]; individuals: unknown[] }
      subject: string
      body: string
      step: string
      send: () => Promise<void>
    }
    vm.recipients = { groups: ['members'], individuals: [] }
    vm.subject = 'Hei'
    vm.body = 'Body'
    vm.step = 'preview'
    await nextTick()

    await vm.send()
    await nextTick()

    expect(fetchApi).toHaveBeenCalledOnce()
    expect(wrapper.text()).toContain('Meldinga er sett i kø')
    expect(wrapper.text()).toContain('5')

    const seeBtn = wrapper.findAll('button').find((b) => b.text().includes('Sjå utsendingar'))
    expect(seeBtn).toBeTruthy()
    await seeBtn!.trigger('click')
    expect(wrapper.emitted('view-broadcasts')).toBeTruthy()
  })
})
