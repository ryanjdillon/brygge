import { foldForSearch, searchTokens } from '@/lib/searchFold'

// The fields account search looks at: the account itself plus the price
// items and categories that book to it by default.
export interface SearchableAccount {
  code: string
  name: string
  price_items?: { name: string }[]
  price_categories?: string[]
}

/**
 * Accounts matching every token of the query in their code, name, price-item
 * names or category labels, so "slipping" finds the account slipping books
 * to. An empty query returns the list unchanged.
 */
export function filterAccounts<T extends SearchableAccount>(
  accounts: T[],
  query: string,
  categoryLabel: (category: string) => string,
): T[] {
  const tokens = searchTokens(query)
  if (!tokens.length) return accounts
  return accounts.filter((a) => {
    const haystack = foldForSearch(
      [
        a.code,
        a.name,
        ...(a.price_items ?? []).map((it) => it.name),
        ...(a.price_categories ?? []).map(categoryLabel),
      ].join(' '),
    )
    return tokens.every((tok) => haystack.includes(tok))
  })
}
