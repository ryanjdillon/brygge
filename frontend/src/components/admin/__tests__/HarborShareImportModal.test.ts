import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import HarborShareImportModal from '@/components/admin/HarborShareImportModal.vue'
import FileInput from '@/components/ui/form/FileInput.vue'
import MemberSearch from '@/components/members/MemberSearch.vue'
import { vBackdropClose } from '@/directives/backdropClose'

const { totpAwareFetch, icon } = vi.hoisted(() => ({
  totpAwareFetch: vi.fn(),
  icon: { template: '<span />' },
}))

vi.mock('lucide-vue-next', () => ({ X: icon, Upload: icon, Search: icon }))
vi.mock('@/composables/useFreshTotp', () => ({
  useFreshTotp: () => ({ ensureFreshTotp: vi.fn().mockResolvedValue(true), totpAwareFetch }),
}))

const counts = (over: Record<string, number> = {}) => ({
  set: 0, date_added: 0, unchanged: 0, conflict: 0, not_owner_but_set: 0,
  duplicate: 0, skipped: 0, unmatched: 0, ambiguous: 0, error: 0, ...over,
})

const preview = {
  dry_run: true,
  plan_token: 'token-1',
  counts: counts({ set: 1, ambiguous: 1, unmatched: 1, conflict: 1 }),
  rows: [
    { row: 2, name: 'Synthetic One', user_id: 'u1', outcome: 'set', deactivated: true, resolved: false,
      current: { owner: false, paid_on: null }, proposed: { owner: true, paid_on: '2026-03-15' } },
    { row: 3, name: 'Same Name', outcome: 'ambiguous', deactivated: false, resolved: false,
      candidates: [
        { user_id: 'c1', name: 'Same Name', slips: ['A10'], deactivated: false },
        { user_id: 'c2', name: 'Same Name', slips: [], deactivated: false },
      ] },
    { row: 4, name: 'Nobody Synthetic', outcome: 'unmatched', deactivated: false, resolved: false },
    { row: 5, name: 'Dated Synthetic', user_id: 'u5', outcome: 'conflict', deactivated: false, resolved: false,
      current: { owner: true, paid_on: '2020-01-01' } },
  ],
}

function respond(status: number, body: unknown) {
  return Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))
}

function sentFields(call: number) {
  const fd = totpAwareFetch.mock.calls[call][1].body as FormData
  return { dryRun: fd.get('dry_run'), resolutions: fd.get('resolutions'), token: fd.get('plan_token') }
}

async function previewed() {
  const wrapper = mountWithPlugins(HarborShareImportModal, {
    props: { open: true },
    global: { stubs: { teleport: true }, directives: { backdropClose: vBackdropClose } },
  })
  const file = new File(['name;harbor_share_owner\n'], 'moloandel.csv', { type: 'text/csv' })
  wrapper.findComponent(FileInput).vm.$emit('change', [file] as unknown as FileList)
  await wrapper.vm.$nextTick()
  await wrapper.get('[data-testid="hsi-preview"]').trigger('click')
  await flushPromises()
  return wrapper
}

describe('HarborShareImportModal', () => {
  beforeEach(() => {
    totpAwareFetch.mockReset()
  })

  it('previews grouped by outcome without applying', async () => {
    totpAwareFetch.mockImplementationOnce(() => respond(200, preview))
    const wrapper = await previewed()

    expect(sentFields(0)).toEqual({ dryRun: 'true', resolutions: null, token: null })
    for (const g of ['set', 'ambiguous', 'unmatched', 'conflict']) {
      expect(wrapper.find(`[data-testid="hsi-group-${g}"]`).exists()).toBe(true)
    }
    expect(wrapper.find('[data-testid="hsi-group-skipped"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="hsi-candidate-3-c1"]').text()).toContain('admin.users.harborShareImport.slips')
    expect(wrapper.get('[data-testid="hsi-candidate-3-c2"]').text()).toContain('admin.users.harborShareImport.noSlip')
    expect(wrapper.text()).toContain('admin.users.harborShareImport.deactivated')
    expect(wrapper.get('[data-testid="hsi-apply"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.findComponent(MemberSearch).exists()).toBe(true)
  })

  it('shows a rejected file without leaving the upload step', async () => {
    totpAwareFetch.mockImplementationOnce(() => respond(400, { error: 'the file is not UTF-8' }))
    const wrapper = await previewed()
    expect(wrapper.find('[data-testid="hsi-upload"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="hsi-error"]').text()).toContain('not UTF-8')
  })

  it('re-previews with the chosen candidate as a resolution', async () => {
    totpAwareFetch
      .mockImplementationOnce(() => respond(200, preview))
      .mockImplementationOnce(() => respond(200, { ...preview, plan_token: 'token-2' }))
    const wrapper = await previewed()
    await wrapper.get('[data-testid="hsi-candidate-3-c1"]').trigger('click')
    await flushPromises()

    expect(sentFields(1)).toEqual({ dryRun: 'true', resolutions: '{"3":"c1"}', token: null })
  })

  it('applies with the plan token and reports the result', async () => {
    totpAwareFetch
      .mockImplementationOnce(() => respond(200, preview))
      .mockImplementationOnce(() => respond(200, { ...preview, dry_run: false }))
    const wrapper = await previewed()
    await wrapper.get('[data-testid="hsi-apply"]').trigger('click')
    await flushPromises()

    expect(sentFields(1)).toEqual({ dryRun: 'false', resolutions: null, token: 'token-1' })
    expect(wrapper.find('[data-testid="hsi-applied"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="hsi-apply"]').exists()).toBe(false)
    expect(wrapper.emitted('applied')).toHaveLength(1)
  })

  it('re-runs the preview when the plan changed', async () => {
    totpAwareFetch
      .mockImplementationOnce(() => respond(200, preview))
      .mockImplementationOnce(() => respond(409, { code: 'plan_changed', error: 'members changed' }))
      .mockImplementationOnce(() => respond(200, { ...preview, plan_token: 'token-3', counts: counts({ unchanged: 1 }),
        rows: [{ ...preview.rows[0], outcome: 'unchanged', proposed: undefined }] }))
    const wrapper = await previewed()
    await wrapper.get('[data-testid="hsi-apply"]').trigger('click')
    await flushPromises()

    expect(totpAwareFetch).toHaveBeenCalledTimes(3)
    expect(sentFields(2).dryRun).toBe('true')
    expect(wrapper.get('[data-testid="hsi-notice"]').text()).toBe('admin.users.harborShareImport.planChanged')
    expect(wrapper.emitted('applied')).toBeUndefined()
    expect(wrapper.get('[data-testid="hsi-apply"]').attributes('disabled')).toBeDefined()
  })
})
