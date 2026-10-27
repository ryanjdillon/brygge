import { describe, it, expect, vi, afterEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import HarborTargetsView from '@/views/admin/HarborTargetsView.vue'

vi.mock('lucide-vue-next', () => {
  const icon = { template: '<span />' }
  return { Plus: icon, FileDown: icon, Pencil: icon, Archive: icon, Copy: icon, QrCode: icon, Printer: icon }
})

const target = {
  id: 'a',
  token: 'toka',
  label: 'Dock A',
  location: '',
  kind: 'guest_slip',
  price_item_id: 'p1',
  item_name: 'Guest night',
  unit_amount: 300,
  currency: 'NOK',
  unit_label: 'natt',
  min_units: 1,
  max_units: 14,
  fixed_units: false,
  archived: false,
  public_url: 'https://club.example.invalid/qr/toka',
  short_code: 'dock-a',
  short_url: 'https://club.example.invalid/q/dock-a',
}

type Res = { ok: boolean; status?: number; body: unknown }

function stubFetch(onWrite: Res = { ok: true, body: { id: 'a' } }) {
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    let res: Res = { ok: true, body: [target] }
    if (url.includes('pricing')) res = { ok: true, body: [{ id: 'p1', name: 'Guest night', amount: 300, category: 'x' }] }
    else if (url.includes('/club')) res = { ok: true, body: { domain: 'club.example.invalid' } }
    else if (init?.method === 'PATCH' || init?.method === 'POST') res = onWrite
    return {
      ok: res.ok,
      status: res.status ?? 200,
      json: async () => res.body,
      text: async () => JSON.stringify(res.body),
    }
  })
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

async function openEditor() {
  const wrapper = mountWithPlugins(HarborTargetsView)
  await flushPromises()
  await wrapper.get('button[title="common.edit"], button[title="Edit"], button[title="Rediger"]').trigger('click')
  return wrapper
}

describe('HarborTargetsView short address', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('lists the short url and shows the live prefix in the editor', async () => {
    stubFetch()
    const wrapper = await openEditor()
    expect(wrapper.get('[data-testid="short-url"]').text()).toContain('https://club.example.invalid/q/dock-a')
    expect(wrapper.get('[data-testid="short-code-prefix"]').text()).toBe('club.example.invalid/q/')
    expect((wrapper.get('[data-testid="short-code-input"]').element as HTMLInputElement).value).toBe('dock-a')
    expect(wrapper.find('[data-testid="short-code-warning"]').exists()).toBe(true)
  })

  it('flags a malformed code and blocks saving', async () => {
    stubFetch()
    const wrapper = await openEditor()
    await wrapper.get('[data-testid="short-code-input"]').setValue('dock a')
    expect(wrapper.find('[data-testid="short-code-error"]').exists()).toBe(true)
    const save = wrapper.findAll('button').find((b) => b.text().match(/common\.save|Save|Lagre/))
    expect(save?.attributes('disabled')).toBeDefined()
  })

  it('shows the server message when the code is taken', async () => {
    const fetchMock = stubFetch({ ok: false, status: 409, body: { error: 'taken', code: 'short_code_taken' } })
    const wrapper = await openEditor()
    await wrapper.get('[data-testid="short-code-input"]').setValue('Gjest')
    const save = wrapper.findAll('button').find((b) => b.text().match(/common\.save|Save|Lagre/))
    await save!.trigger('click')
    await flushPromises()
    const patch = fetchMock.mock.calls.find(([, init]) => init?.method === 'PATCH')
    expect(JSON.parse(String(patch?.[1]?.body)).short_code).toBe('gjest')
    expect(wrapper.find('[data-testid="short-code-error"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="short-code-input"]').exists()).toBe(true)
  })
})
