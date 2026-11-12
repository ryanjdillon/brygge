import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { ref } from 'vue'
import { mountWithPlugins } from '@/test/test-utils'
import JournalEntryForm from '@/views/admin/JournalEntryForm.vue'

// A bilag is posted only with its receipt: when the upload fails the draft
// is kept, nothing is posted, and a retry reuses that draft (BRY-675).
const { ensureFreshTotp, create, upload, post, parse, icon, field } = vi.hoisted(() => ({
  field: (kind: string, numeric: boolean) => ({
    props: ['modelValue'],
    emits: ['update:modelValue'],
    template: `<input data-stub="${kind}" :value="modelValue"
      @input="$emit('update:modelValue', ${numeric ? 'Number($event.target.value)' : '$event.target.value'})" />`,
  }),
  ensureFreshTotp: vi.fn(),
  create: vi.fn(),
  upload: vi.fn(),
  post: vi.fn(),
  parse: vi.fn(),
  icon: { template: '<span />' },
}))

vi.mock('lucide-vue-next', () => ({
  ArrowLeft: icon, Plus: icon, Trash2: icon, Check: icon, X: icon, Info: icon, Paperclip: icon,
}))
vi.mock('@/composables/useFreshTotp', () => ({ useFreshTotp: () => ({ ensureFreshTotp }) }))
// Form fields as plain inputs, so the test fills them directly.
vi.mock('@/components/ui/form/Select.vue', () => ({ default: field('select', false) }))
vi.mock('@/components/ui/form/Input.vue', () => ({ default: field('input', false) }))
vi.mock('@/components/ui/form/NumberInput.vue', () => ({ default: field('number', true) }))
vi.mock('@/components/ui/form/DateInput.vue', () => ({ default: field('date', false) }))
vi.mock('@/composables/useAccounting', () => ({
  useAccountsList: () => ({
    data: ref([
      { code: '1920', name: 'Bank', account_type: 'asset' },
      { code: '6300', name: 'Leie', account_type: 'expense' },
    ]),
  }),
  useFiscalPeriods: () => ({ data: ref([{ id: 'p1', year: 2026, status: 'open', reference_only: false }]) }),
  useCreateJournalEntry: () => ({ mutateAsync: create, isPending: ref(false) }),
  usePostEntry: () => ({ mutateAsync: post, isPending: ref(false) }),
  useUploadJournalAttachment: () => ({ mutateAsync: upload, isPending: ref(false) }),
  useParseReceipt: () => ({ mutate: parse }),
}))

async function fillForm(wrapper: ReturnType<typeof mountWithPlugins>) {
  await wrapper.get('[data-stub="input"]').setValue('Synthetic rent')
  await wrapper.findAll('[data-stub="select"]')[1]!.setValue('6300')
  await wrapper.findAll('[data-stub="number"]')[0]!.setValue('100')
  await wrapper.findAll('button').find(b => b.text().includes('admin.accounting.journalForm.addLine'))!.trigger('click')
  await wrapper.findAll('[data-stub="select"]')[2]!.setValue('1920')
  // Line 1: debit, credit, mva (an expense account); line 2: debit, credit.
  await wrapper.findAll('[data-stub="number"]')[4]!.setValue('100')
  const file = new File(['%PDF-1.4'], 'kvittering.pdf', { type: 'application/pdf' })
  const input = wrapper.get('[data-testid="receipt-file"]')
  Object.defineProperty(input.element, 'files', { value: [file] })
  await input.trigger('change')
}

describe('JournalEntryForm receipt upload', () => {
  beforeEach(() => {
    ensureFreshTotp.mockReset().mockResolvedValue(true)
    create.mockReset().mockResolvedValue({ id: 'draft-1' })
    upload.mockReset()
    post.mockReset().mockResolvedValue({ message: 'ok' })
    // Receipt reading is not set up: the file is attached, nothing is filled.
    parse.mockReset().mockImplementation((_f: File, h: { onError: (e: Error) => void }) => h.onError(new Error('no key')))
  })

  it('does not post when the receipt upload fails, and retries on the same draft', async () => {
    upload.mockRejectedValueOnce(new Error('upload failed'))
    const wrapper = mountWithPlugins(JournalEntryForm)
    await flushPromises()
    await fillForm(wrapper)

    await wrapper.get('[data-testid="post-entry"]').trigger('click')
    await flushPromises()

    expect(create).toHaveBeenCalledTimes(1)
    expect(upload).toHaveBeenCalledTimes(1)
    expect(post).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="receipt-upload-error"]').text())
      .toBe('admin.accounting.journalForm.receiptUploadFailedDraftKept')
    expect(wrapper.findAll('fieldset').every(f => f.attributes('disabled') !== undefined)).toBe(true)

    upload.mockResolvedValueOnce({ attachment_url: 'k' })
    await wrapper.get('[data-testid="post-entry"]').trigger('click')
    await flushPromises()

    expect(create).toHaveBeenCalledTimes(1)
    expect(upload).toHaveBeenLastCalledWith(expect.objectContaining({ entryId: 'draft-1' }))
    expect(post).toHaveBeenCalledWith('draft-1')
    expect(ensureFreshTotp).toHaveBeenCalledTimes(2)
  })

  it('posts after a successful upload', async () => {
    upload.mockResolvedValue({ attachment_url: 'k' })
    const wrapper = mountWithPlugins(JournalEntryForm)
    await flushPromises()
    await fillForm(wrapper)

    await wrapper.get('[data-testid="post-entry"]').trigger('click')
    await flushPromises()

    expect(upload).toHaveBeenCalledWith(expect.objectContaining({ entryId: 'draft-1' }))
    expect(post).toHaveBeenCalledWith('draft-1')
    expect(wrapper.find('[data-testid="receipt-upload-error"]').exists()).toBe(false)
  })

  it('keeps the form when saving a draft whose upload fails', async () => {
    upload.mockRejectedValueOnce(new Error('upload failed'))
    const wrapper = mountWithPlugins(JournalEntryForm)
    await flushPromises()
    await fillForm(wrapper)

    await wrapper.get('[data-testid="save-draft"]').trigger('click')
    await flushPromises()

    expect(post).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="receipt-upload-error"]').exists()).toBe(true)
  })
})
