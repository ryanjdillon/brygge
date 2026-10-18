import { describe, it, expect } from 'vitest'
import { foldForSearch, searchTokens } from '@/lib/searchFold'

describe('foldForSearch', () => {
  it('folds case, Norwegian letters and accents', () => {
    expect(foldForSearch('Slipping LITEN Båt')).toBe('slipping liten bat')
    expect(foldForSearch('Ærfugl Øy')).toBe('aerfugl oy')
    expect(foldForSearch('Café Crème')).toBe('cafe creme')
  })

  it('turns word-separating punctuation into single spaces', () => {
    expect(foldForSearch('  Salgs-  og_tjeneste.inntekter, 3300 ')).toBe('salgs og tjeneste inntekter 3300')
  })

  it('splits a query into folded tokens', () => {
    expect(searchTokens(' Slipping  BÅT ')).toEqual(['slipping', 'bat'])
    expect(searchTokens('   ')).toEqual([])
  })
})
