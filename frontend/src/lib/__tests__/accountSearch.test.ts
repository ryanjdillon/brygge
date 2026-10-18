import { describe, it, expect } from 'vitest'
import { filterAccounts } from '@/lib/accountSearch'

const labels: Record<string, string> = { service: 'Tenester', guest: 'Gjesteplassar' }
const label = (c: string) => labels[c] ?? c

const accounts = [
  {
    code: '3300',
    name: 'Salgsinntekter',
    price_items: [{ name: 'Slipping liten båt' }, { name: 'Slipping stor båt' }],
    price_categories: ['service'],
  },
  { code: '3200', name: 'Gjestehavninntekter', price_items: [{ name: 'Gjesteplass' }], price_categories: ['guest'] },
  { code: '3900', name: 'Andre inntekter' },
]
const codes = (q: string) => filterAccounts(accounts, q, label).map((a) => a.code)

describe('filterAccounts', () => {
  it('finds an account by the name of a price item that books to it', () => {
    expect(codes('slipping')).toEqual(['3300'])
    expect(codes('SLIPPING')).toEqual(['3300'])
    expect(codes('stor bat')).toEqual(['3300'])
  })

  it('finds an account by its category label', () => {
    expect(codes('tenester')).toEqual(['3300'])
  })

  it('requires every word to match', () => {
    expect(codes('slipping gjesteplass')).toEqual([])
  })

  it('still searches code and name', () => {
    expect(codes('39')).toEqual(['3900'])
    expect(codes('andre')).toEqual(['3900'])
    expect(codes('')).toEqual(['3300', '3200', '3900'])
  })
})
