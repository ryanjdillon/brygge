import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useClubStore } from '@/stores/club'

function serve(info: Record<string, unknown>) {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(info), { status: 200 })))
}

describe('club store', () => {
  beforeEach(() => setActivePinia(createPinia()))
  afterEach(() => vi.unstubAllGlobals())

  it('loads the public description from the public club info, trimmed', async () => {
    serve({ name: 'Synthetic Club', slug: 'synthetic', domain: 'example.test', public_description: '  Boats and coffee ' })
    const club = useClubStore()
    await club.ensureLoaded()
    expect(club.name).toBe('Synthetic Club')
    expect(club.publicDescription).toBe('Boats and coffee')
    expect(club.settled).toBe(true)
    expect(club.failed).toBe(false)
  })

  it('is not settled before the load finishes', async () => {
    let answer: (r: Response) => void = () => {}
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>((resolve) => (answer = resolve))))
    const club = useClubStore()
    const pending = club.ensureLoaded()
    expect(club.settled).toBe(false)
    answer(new Response(JSON.stringify({ name: 'Synthetic Club', public_description: 'Hi' }), { status: 200 }))
    await pending
    expect(club.settled).toBe(true)
  })

  it('has no public description when the club set none', async () => {
    serve({ name: 'Synthetic Club', slug: 'synthetic', domain: 'example.test' })
    const club = useClubStore()
    await club.ensureLoaded()
    expect(club.publicDescription).toBe('')
  })

  it('keeps it empty when the club info cannot load', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('', { status: 500 })))
    const club = useClubStore()
    await club.ensureLoaded()
    expect(club.publicDescription).toBe('')
    expect(club.loaded).toBe(false)
    expect(club.settled).toBe(true)
    expect(club.failed).toBe(true)
  })

  it('settles as failed when the network is down', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('Failed to fetch') }))
    const club = useClubStore()
    await club.ensureLoaded()
    expect(club.settled).toBe(true)
    expect(club.failed).toBe(true)
    expect(club.publicDescription).toBe('')
  })
})
