// foldForSearch reduces text to a form where a treasurer's query matches
// regardless of case, Norwegian letters or accents: "Slipping <26 fot" and
// "slipping 26" meet, and "Tenester" matches "tenester". Punctuation that
// separates words in names (- _ . ,) becomes a space.
export function foldForSearch(s: string): string {
  return s
    .toLowerCase()
    .replace(/æ/g, 'ae')
    .replace(/ø/g, 'o')
    .replace(/å/g, 'a')
    .normalize('NFD')
    .replace(/[̀-ͯ]/g, '')
    .replace(/[-_.,]/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
}

/** Query tokens after folding; an empty query has none. */
export function searchTokens(query: string): string[] {
  const folded = foldForSearch(query)
  return folded ? folded.split(' ') : []
}
