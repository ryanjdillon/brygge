import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import { useToast } from '@/composables/useToast'
import PaymentFiltersView from '@/views/admin/PaymentFiltersView.vue'

const client = { GET: vi.fn(), POST: vi.fn(), PATCH: vi.fn(), PUT: vi.fn(), DELETE: vi.fn() }
const askConfirm = vi.fn()

vi.mock('@/lib/apiClient', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/apiClient')>()
  return { ...actual, useApiClient: () => client }
})
vi.mock('@/stores/confirm', () => ({ useConfirm: () => askConfirm }))

const ok = <T>(data: T) => ({ data, response: { ok: true, status: 200, statusText: 'OK' } })
const refused = (status: number, code: string) => ({
  error: { error: code, code },
  response: { ok: false, status, statusText: 'Refused' },
})

const group = (slug: string, extra: Record<string, unknown> = {}) => ({
  slug,
  name: null,
  label_key: null,
  icon: 'anchor',
  sort_order: 10,
  enabled: true,
  builtin: null,
  price_item_ids: [] as string[],
  item_count: 0,
  ...extra,
})

const item = (id: string, extra: Record<string, unknown> = {}) => ({
  id,
  name: `Synthetic ${id}`,
  category: 'service',
  is_active: true,
  group_slug: null,
  qr_labels: [] as string[],
  order_reachable: true,
  sold: false,
  ...extra,
})

function fixture() {
  return {
    groups: [
      group('guest', { label_key: 'guest', icon: 'sailboat', sort_order: 10, price_item_ids: ['i1'], item_count: 1 }),
      group('slipping', { name: 'Slipping', icon: 'forklift', sort_order: 20, price_item_ids: ['i2'], item_count: 1 }),
      group('faktura', { label_key: 'faktura', icon: 'file-text', sort_order: 40, builtin: 'faktura', enabled: false }),
      group('other', { label_key: 'other', icon: 'circle-dollar-sign', sort_order: 1000, builtin: 'other' }),
    ],
    price_items: [
      item('i1', { category: 'guest', group_slug: 'guest' }),
      item('i2', { group_slug: 'slipping', qr_labels: ['Crane poster'] }),
      item('i3', { sold: true }),
      item('i4', { category: 'membership', order_reachable: false }),
      item('i5', { category: 'guest', is_active: false, sold: true }),
      // Reachable but never sold: in the picker, not in the unassigned panel.
      item('i6'),
    ],
    icons: [],
  }
}

const asWriter = { initialState: { auth: { user: { roles: ['harbor_master'] } } } }
const asReader = { initialState: { auth: { user: { roles: ['chair'] } } } }

async function mountView(piniaOptions = asWriter) {
  const wrapper = mountWithPlugins(PaymentFiltersView, { piniaOptions, global: { stubs: { Teleport: true } } })
  await flushPromises()
  return wrapper
}

type Wrapper = Awaited<ReturnType<typeof mountView>>
const cards = (w: Wrapper) => w.findAll('[data-testid="filter-group"]')
const card = (w: Wrapper, slug: string) => w.find(`[data-testid="filter-group"][data-slug="${slug}"]`)
const editor = (w: Wrapper) => w.find('[data-testid="group-editor"]')
const toasts = () => useToast().toasts.value.map((t) => `${t.type}:${t.message}`)

async function openNew(w: Wrapper) {
  await w.find('[data-testid="new-group"]').trigger('click')
  await flushPromises()
}

async function openEdit(w: Wrapper, slug: string) {
  await card(w, slug).find('[data-testid="edit-group"]').trigger('click')
  await flushPromises()
}

async function typeName(w: Wrapper, name: string) {
  await w.find('[data-testid="name-input"]').setValue(name)
}

async function save(w: Wrapper) {
  await w.find('[data-testid="group-editor"]').trigger('submit')
  await flushPromises()
}

beforeEach(() => {
  vi.clearAllMocks()
  askConfirm.mockResolvedValue(true)
  client.GET.mockResolvedValue(ok(fixture()))
  for (const m of [client.POST, client.PATCH, client.PUT, client.DELETE]) m.mockResolvedValue(ok({ group: group('x'), moved: [] }))
})

describe('PaymentFiltersView list', () => {
  it('lists the groups in the order the server sent, with label, count and state', async () => {
    const w = await mountView()
    expect(cards(w).map((c) => c.attributes('data-slug'))).toEqual(['guest', 'slipping', 'faktura', 'other'])
    // A typed name is shown as typed; a seeded row shows its translation key.
    expect(card(w, 'slipping').find('[data-testid="group-label"]').text()).toBe('Slipping')
    expect(card(w, 'guest').find('[data-testid="group-label"]').text()).toBe('recentPayments.kind.guest')
    expect(card(w, 'guest').text()).toContain('paymentFilters.itemCount')
    expect(card(w, 'faktura').text()).toContain('paymentFilters.builtinFaktura')
    expect(card(w, 'faktura').find('[data-testid="hidden-badge"]').exists()).toBe(true)
    expect(card(w, 'other').text()).toContain('paymentFilters.builtinOther')
    expect(card(w, 'guest').find('[data-testid="filter-group-icon"]').attributes('data-icon')).toBe('sailboat')
  })

  it('lists the active prices that were sold and are in no group, not archived, unsold or billing-only ones', async () => {
    const w = await mountView()
    expect(w.find('[data-testid="unassigned-count"]').text()).toBe('(1)')
    expect(w.findAll('[data-testid="unassigned-item"]').map((i) => i.text())).toEqual(['Synthetic i3'])
  })

  it('says every price is in a group when none is left', async () => {
    const data = fixture()
    data.price_items = data.price_items.filter((p) => p.id !== 'i3')
    client.GET.mockResolvedValue(ok(data))
    const w = await mountView()
    expect(w.find('[data-testid="unassigned-panel"]').text()).toContain('paymentFilters.unassignedNone')
  })

  it('shows an error with a retry, not an empty list, when the load fails', async () => {
    client.GET.mockResolvedValueOnce(refused(500, 'boom'))
    const w = await mountView()
    expect(w.find('[data-testid="load-error"]').exists()).toBe(true)
    expect(cards(w)).toHaveLength(0)

    await w.find('[data-testid="load-retry"]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid="load-error"]').exists()).toBe(false)
    expect(cards(w)).toHaveLength(4)
  })

  it('has a back link to the Payments page as a 44px target', async () => {
    const w = await mountView()
    const back = w.find('[data-testid="back-link"]')
    expect(back.attributes('href')).toBe('/admin/harbor/recent-payments')
    expect(back.classes()).toContain('min-h-11')
  })

  it('stacks the controls of a card and keeps every control at least 44px', async () => {
    const w = await mountView()
    const c = card(w, 'slipping')
    for (const sel of ['[data-testid="enabled-switch"]', '[data-testid="move-up"]', '[data-testid="move-down"]', '[data-testid="edit-group"]']) {
      expect(c.find(sel).classes()).toContain('min-h-11')
    }
    expect(c.find('div.flex-wrap').exists()).toBe(true)
    expect(w.find('[data-testid="new-group"]').classes()).toContain('min-h-11')
  })
})

describe('PaymentFiltersView read-only', () => {
  it('lets a viewer see the groups but not change them', async () => {
    const w = await mountView(asReader)
    expect(w.find('[data-testid="read-only-note"]').exists()).toBe(true)
    expect(cards(w)).toHaveLength(4)
    expect(w.find('[data-testid="new-group"]').exists()).toBe(false)
    expect(w.find('[data-testid="edit-group"]').exists()).toBe(false)
    expect(w.find('[data-testid="move-up"]').exists()).toBe(false)
    expect(w.find('fieldset').attributes('disabled')).toBeDefined()
    expect(card(w, 'slipping').find('[data-testid="enabled-switch"]').attributes('disabled')).toBeDefined()
    expect(w.find('[data-testid="unassigned-panel"]').exists()).toBe(true)
  })

  it('shows the controls to the roles that may change the groups', async () => {
    for (const role of ['board', 'harbor_master', 'treasurer', 'admin']) {
      const w = await mountView({ initialState: { auth: { user: { roles: [role] } } } })
      expect(w.find('[data-testid="new-group"]').exists(), role).toBe(true)
      expect(w.find('[data-testid="read-only-note"]').exists(), role).toBe(false)
    }
  })
})

describe('PaymentFiltersView order and switch', () => {
  it('saves the new order at once, with the fallback left out', async () => {
    client.PUT.mockResolvedValue(
      ok({ groups: [group('slipping'), group('guest'), group('faktura', { builtin: 'faktura' }), group('other', { builtin: 'other' })] }),
    )
    const w = await mountView()
    await card(w, 'guest').find('[data-testid="move-down"]').trigger('click')
    await flushPromises()
    expect(client.PUT).toHaveBeenCalledWith('/api/v1/admin/payment-filter-groups/order', { body: { slugs: ['slipping', 'guest', 'faktura'] } })
    expect(cards(w).map((c) => c.attributes('data-slug'))).toEqual(['slipping', 'guest', 'faktura', 'other'])
  })

  it('has no way to move the first group up, the last movable group down, or the fallback at all', async () => {
    const w = await mountView()
    expect(card(w, 'guest').find('[data-testid="move-up"]').attributes('disabled')).toBeDefined()
    expect(card(w, 'faktura').find('[data-testid="move-down"]').attributes('disabled')).toBeDefined()
    expect(card(w, 'other').find('[data-testid="move-up"]').exists()).toBe(false)
    expect(card(w, 'other').find('[data-testid="enabled-switch"]').exists()).toBe(false)
  })

  it('disables the move buttons while an order is being saved', async () => {
    let finish: (v: unknown) => void = () => {}
    client.PUT.mockReturnValue(new Promise((resolve) => (finish = resolve)))
    const w = await mountView()
    await card(w, 'guest').find('[data-testid="move-down"]').trigger('click')
    await flushPromises()
    for (const b of w.findAll('[data-testid="move-up"], [data-testid="move-down"]')) {
      expect(b.attributes('disabled')).toBeDefined()
    }
    // A second tap while the first is in flight sends nothing.
    await card(w, 'faktura').find('[data-testid="move-up"]').trigger('click')
    expect(client.PUT).toHaveBeenCalledTimes(1)

    finish(ok({ groups: fixture().groups }))
    await flushPromises()
    expect(card(w, 'faktura').find('[data-testid="move-up"]').attributes('disabled')).toBeUndefined()
  })

  it('puts the list back, says why and reads it again when the order is refused', async () => {
    client.PUT.mockResolvedValue(refused(400, 'order_mismatch'))
    const w = await mountView()
    client.GET.mockClear()
    await card(w, 'guest').find('[data-testid="move-down"]').trigger('click')
    await flushPromises()
    expect(cards(w).map((c) => c.attributes('data-slug'))).toEqual(['guest', 'slipping', 'faktura', 'other'])
    expect(client.GET).toHaveBeenCalledTimes(1)
    expect(toasts()).toContain('error:paymentFilters.errors.order_mismatch')
  })

  it('puts the list back on any other failure too', async () => {
    client.PUT.mockResolvedValue(refused(500, 'boom'))
    const w = await mountView()
    await card(w, 'guest').find('[data-testid="move-down"]').trigger('click')
    await flushPromises()
    expect(cards(w).map((c) => c.attributes('data-slug'))).toEqual(['guest', 'slipping', 'faktura', 'other'])
    expect(toasts()).toContain('error:paymentFilters.errors.generic')
  })

  it('turns a group off and on from its switch', async () => {
    const w = await mountView()
    const sw = card(w, 'slipping').find('[data-testid="enabled-switch"]')
    expect(sw.attributes('role')).toBe('switch')
    expect(sw.attributes('aria-checked')).toBe('true')
    await sw.trigger('click')
    await flushPromises()
    expect(client.PATCH).toHaveBeenCalledWith('/api/v1/admin/payment-filter-groups/{slug}', {
      params: { path: { slug: 'slipping' } },
      body: { enabled: false },
    })

    await card(w, 'faktura').find('[data-testid="enabled-switch"]').trigger('click')
    await flushPromises()
    expect(client.PATCH).toHaveBeenLastCalledWith('/api/v1/admin/payment-filter-groups/{slug}', {
      params: { path: { slug: 'faktura' } },
      body: { enabled: true },
    })
  })
})

describe('PaymentFiltersView editor', () => {
  it('opens full screen on phones with a name field, an icon grid and the prices', async () => {
    const w = await mountView()
    await openNew(w)
    expect(editor(w).exists()).toBe(true)
    // Modal's fullScreenOnMobile panel: full height and width below md.
    expect(w.find('.h-full.w-full').exists()).toBe(true)
    expect(w.find('[data-testid="name-input"]').attributes('maxlength')).toBe('40')
    expect(w.find('[data-testid="name-input"]').classes()).toContain('min-h-11')
    const radios = w.findAll('[data-testid="icon-grid"] input[type="radio"]')
    expect(radios.length).toBe(14)
    for (const r of radios) expect(r.attributes('aria-label')).toMatch(/^paymentFilters\.icons\./)
    for (const l of w.findAll('[data-testid="icon-grid"] label')) {
      expect(l.classes()).toEqual(expect.arrayContaining(['h-11', 'w-11']))
    }
    for (const sel of ['[data-testid="save-group"]', '[data-testid="cancel-editor"]']) {
      expect(w.find(sel).classes()).toContain('min-h-11')
    }
  })

  it('creates a group with a name, an icon and prices', async () => {
    const w = await mountView()
    await openNew(w)
    await typeName(w, '  Kran  ')
    await w.find('[data-testid="icon-grid"] input[value="wrench"]').setValue(true)
    await w.findAll('input[type="checkbox"]').find((c) => c.element.closest('label')?.textContent?.includes('Synthetic i3'))!.trigger('change')
    await save(w)
    expect(client.POST).toHaveBeenCalledWith('/api/v1/admin/payment-filter-groups', {
      body: { name: 'Kran', icon: 'wrench', price_item_ids: ['i3'] },
    })
    expect(askConfirm).not.toHaveBeenCalled()
    expect(editor(w).exists()).toBe(false)
    expect(toasts()).toContain('success:paymentFilters.saved')
  })

  it('needs a name before it creates a group', async () => {
    const w = await mountView()
    await openNew(w)
    await save(w)
    expect(client.POST).not.toHaveBeenCalled()
    expect(w.find('[data-testid="form-error"]').text()).toBe('paymentFilters.errors.invalid_name')
  })

  it('refuses a name another chip already shows, seeded translations included', async () => {
    const w = await mountView()
    await openNew(w)
    await typeName(w, 'slipping')
    await save(w)
    expect(client.POST).not.toHaveBeenCalled()
    expect(w.find('[data-testid="form-error"]').text()).toBe('paymentFilters.errors.name_taken')

    // The seeded guest group shows its translation; so does the mocked t().
    await typeName(w, 'recentPayments.kind.guest')
    await save(w)
    expect(client.POST).not.toHaveBeenCalled()
  })

  it('shows what the server refused, by its code', async () => {
    client.POST.mockResolvedValue(refused(409, 'too_many_groups'))
    const w = await mountView()
    await openNew(w)
    await typeName(w, 'Kran')
    await save(w)
    expect(w.find('[data-testid="form-error"]').text()).toBe('paymentFilters.errors.too_many_groups')
    expect(editor(w).exists()).toBe(true)
  })

  it('renames a group, and sends nothing when nothing changed', async () => {
    const w = await mountView()
    await openEdit(w, 'slipping')
    expect((w.find('[data-testid="name-input"]').element as HTMLInputElement).value).toBe('Slipping')
    await save(w)
    expect(client.PATCH).not.toHaveBeenCalled()
    expect(client.PUT).not.toHaveBeenCalled()

    await openEdit(w, 'slipping')
    await typeName(w, 'Haul-out')
    await save(w)
    expect(client.PATCH).toHaveBeenCalledWith('/api/v1/admin/payment-filter-groups/{slug}', {
      params: { path: { slug: 'slipping' } },
      body: { name: 'Haul-out' },
    })
  })

  it('goes back to the translated default name of a seeded group', async () => {
    const data = fixture()
    data.groups[2] = group('faktura', { name: 'Fakturaer', label_key: 'faktura', builtin: 'faktura' })
    client.GET.mockResolvedValue(ok(data))
    const w = await mountView()
    await openEdit(w, 'faktura')
    await w.find('[data-testid="reset-name"]').trigger('click')
    expect(w.find('[data-testid="reset-name"]').exists()).toBe(false)
    await save(w)
    expect(client.PATCH).toHaveBeenCalledWith('/api/v1/admin/payment-filter-groups/{slug}', {
      params: { path: { slug: 'faktura' } },
      body: { reset_name: true },
    })
  })

  it('offers no default name for a group without one, and none until a name is set on a seeded one', async () => {
    const w = await mountView()
    await openEdit(w, 'slipping')
    expect(w.find('[data-testid="reset-name"]').exists()).toBe(false)
    await w.find('[data-testid="cancel-editor"]').trigger('click')
    await openEdit(w, 'guest')
    expect(w.find('[data-testid="reset-name"]').exists()).toBe(false)
    expect((w.find('[data-testid="name-input"]').element as HTMLInputElement).placeholder).toBe('recentPayments.kind.guest')
  })

  it('changes the icon', async () => {
    const w = await mountView()
    await openEdit(w, 'slipping')
    await w.find('[data-testid="icon-grid"] input[value="tent"]').setValue(true)
    await save(w)
    expect(client.PATCH).toHaveBeenCalledWith('/api/v1/admin/payment-filter-groups/{slug}', {
      params: { path: { slug: 'slipping' } },
      body: { icon: 'tent' },
    })
  })

  it('has no price list for a built-in group', async () => {
    const w = await mountView()
    await openEdit(w, 'faktura')
    expect(w.find('[data-testid="group-editor"] ul').exists()).toBe(false)
    expect(w.find('[data-testid="delete-group"]').exists()).toBe(false)
  })

  it('hints at what a price is: category, archived, QR posters, faktura only', async () => {
    const w = await mountView()
    await openEdit(w, 'slipping')
    const text = editor(w).text()
    expect(text).toContain('paymentFilters.editor.qr')
    expect(text).toContain('paymentFilters.editor.archived')
    expect(text).toContain('paymentFilters.editor.fakturaOnly')
    expect(text).toContain('admin.pricing.categoryMembership')
    // The price that cannot reach an order is listed last.
    const names = editor(w).findAll('li label span.truncate').map((s) => s.text())
    expect(names[names.length - 1]).toBe('Synthetic i4')
  })
})

describe('PaymentFiltersView moving prices', () => {
  it('confirms before a price leaves another group, listing the moves', async () => {
    const w = await mountView()
    await openEdit(w, 'slipping')
    await w.findAll('input[type="checkbox"]').find((c) => c.element.closest('label')?.textContent?.includes('Synthetic i1'))!.trigger('change')
    await save(w)
    expect(askConfirm).toHaveBeenCalledTimes(1)
    const asked = askConfirm.mock.calls[0][0]
    expect(asked.details).toEqual(['Synthetic i1: recentPayments.kind.guest → Slipping'])
    expect(asked.body).toContain('paymentFilters.moveConfirm.body')
    expect(client.PUT).toHaveBeenCalledWith('/api/v1/admin/payment-filter-groups/{slug}/items', {
      params: { path: { slug: 'slipping' } },
      body: { price_item_ids: expect.arrayContaining(['i1', 'i2']) },
    })
  })

  it('writes nothing when the move is declined', async () => {
    askConfirm.mockResolvedValue(false)
    const w = await mountView()
    await openEdit(w, 'slipping')
    await w.findAll('input[type="checkbox"]').find((c) => c.element.closest('label')?.textContent?.includes('Synthetic i1'))!.trigger('change')
    await save(w)
    expect(client.PUT).not.toHaveBeenCalled()
    expect(client.PATCH).not.toHaveBeenCalled()
    expect(editor(w).exists()).toBe(true)
  })

  it('does not ask when only unassigned prices are added, or when one is removed', async () => {
    const w = await mountView()
    await openEdit(w, 'slipping')
    const tick = (name: string) =>
      w.findAll('input[type="checkbox"]').find((c) => c.element.closest('label')?.textContent?.includes(name))!.trigger('change')
    await tick('Synthetic i3')
    await tick('Synthetic i2')
    await save(w)
    expect(askConfirm).not.toHaveBeenCalled()
    expect(client.PUT).toHaveBeenCalledWith('/api/v1/admin/payment-filter-groups/{slug}/items', {
      params: { path: { slug: 'slipping' } },
      body: { price_item_ids: ['i3'] },
    })
  })

  it('selects only the prices nobody holds with "All", never moving one out of another group', async () => {
    const w = await mountView()
    await openNew(w)
    await typeName(w, 'Rest')
    const toggle = w.find('[data-testid="multi-select-toggle-all"]')
    expect(toggle.classes()).toEqual(expect.arrayContaining(['min-h-11', 'min-w-11']))
    expect(toggle.text()).toBe('common.selectAll')
    await toggle.trigger('click')
    await save(w)
    // i3, i5 (archived) and i6 are unassigned and can reach an order; i1 and i2
    // are in groups, i4 is faktura only.
    expect(askConfirm).not.toHaveBeenCalled()
    const body = client.POST.mock.calls[0][1].body
    expect([...body.price_item_ids].sort()).toEqual(['i3', 'i5', 'i6'])
  })
})

describe('PaymentFiltersView saving', () => {
  it('starts one save only, even if submitted again while the move confirm is open', async () => {
    let answer: (ok: boolean) => void = () => {}
    askConfirm.mockReturnValue(new Promise<boolean>((resolve) => (answer = resolve)))
    const w = await mountView()
    await openEdit(w, 'slipping')
    const tick = (name: string) =>
      w.findAll('input[type="checkbox"]').find((c) => c.element.closest('label')?.textContent?.includes(name))!.trigger('change')
    await tick('Synthetic i1')
    await w.find('[data-testid="group-editor"]').trigger('submit')
    await w.find('[data-testid="group-editor"]').trigger('submit')
    await w.find('[data-testid="save-group"]').trigger('click')
    await flushPromises()
    expect(askConfirm).toHaveBeenCalledTimes(1)
    expect(w.find('[data-testid="save-group"]').attributes('disabled')).toBeDefined()

    answer(true)
    await flushPromises()
    expect(client.PUT).toHaveBeenCalledTimes(1)
    expect(w.find('[data-testid="save-group"]').exists()).toBe(false)
  })

  it('can be saved again after declining the move', async () => {
    askConfirm.mockResolvedValueOnce(false)
    const w = await mountView()
    await openEdit(w, 'slipping')
    await w.findAll('input[type="checkbox"]').find((c) => c.element.closest('label')?.textContent?.includes('Synthetic i1'))!.trigger('change')
    await save(w)
    expect(w.find('[data-testid="save-group"]').attributes('disabled')).toBeUndefined()
    askConfirm.mockResolvedValue(true)
    await save(w)
    expect(client.PUT).toHaveBeenCalledTimes(1)
  })

  it('refuses the name of the no-filter chip before it asks the server', async () => {
    const w = await mountView()
    await openNew(w)
    await typeName(w, 'recentPayments.kind.all')
    await save(w)
    expect(client.POST).not.toHaveBeenCalled()
    expect(w.find('[data-testid="form-error"]').text()).toBe('paymentFilters.errors.reserved_name')
  })

  it('says one price or many in the confirm, by the number moved', async () => {
    const w = await mountView()
    await openEdit(w, 'slipping')
    await w.findAll('input[type="checkbox"]').find((c) => c.element.closest('label')?.textContent?.includes('Synthetic i1'))!.trigger('change')
    await save(w)
    // The stub t() ignores the plural count; the call passes it as the third argument.
    expect(askConfirm.mock.calls[0][0].body).toContain('paymentFilters.moveConfirm.body')
  })
})

describe('PaymentFiltersView delete', () => {
  it('confirms with the number of prices that become unassigned, then deletes', async () => {
    const w = await mountView()
    await openEdit(w, 'slipping')
    await w.find('[data-testid="delete-group"]').trigger('click')
    await flushPromises()
    expect(askConfirm).toHaveBeenCalledTimes(1)
    const asked = askConfirm.mock.calls[0][0]
    expect(asked.tone).toBe('danger')
    expect(asked.title).toContain('paymentFilters.delete.title')
    expect(asked.body).toContain('paymentFilters.delete.body')
    expect(client.DELETE).toHaveBeenCalledWith('/api/v1/admin/payment-filter-groups/{slug}', { params: { path: { slug: 'slipping' } } })
    expect(editor(w).exists()).toBe(false)
  })

  it('deletes nothing when declined', async () => {
    askConfirm.mockResolvedValue(false)
    const w = await mountView()
    await openEdit(w, 'slipping')
    await w.find('[data-testid="delete-group"]').trigger('click')
    await flushPromises()
    expect(client.DELETE).not.toHaveBeenCalled()
    expect(editor(w).exists()).toBe(true)
  })
})
