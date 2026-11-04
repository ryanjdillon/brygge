import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountWithPlugins } from '@/test/test-utils'
import MemberSearch from '@/components/members/MemberSearch.vue'

vi.mock('lucide-vue-next', () => ({ X: { template: '<span />' }, Search: { template: '<span />' } }))

const fetchMock = vi.fn()

async function search(props: Record<string, unknown>) {
  const wrapper = mountWithPlugins(MemberSearch, { props: { modelValue: null, ...props } })
  await wrapper.get('input').setValue('Ada Synthetic')
  vi.advanceTimersByTime(300)
  await flushPromises()
  return String(fetchMock.mock.calls[0][0])
}

describe('MemberSearch', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    fetchMock.mockReset().mockResolvedValue(new Response(JSON.stringify({ items: [] }), { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('keeps the server default status when none is given', async () => {
    expect(await search({})).toBe('/api/v1/admin/users?q=Ada%20Synthetic&limit=20')
  })

  it('passes the status filter when given', async () => {
    expect(await search({ status: 'all' })).toBe('/api/v1/admin/users?q=Ada%20Synthetic&limit=20&status=all')
  })
})
