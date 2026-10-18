// The price_items.category and unit vocabularies with their label keys,
// shared by the pricing views and the account hints in Tildel. The category
// order is the one the pricing admin lists; it matches the backend's
// accounting.PriceItemCategories.

export const PRICE_CATEGORIES = [
  'membership',
  'harbor_membership',
  'slip_fee',
  'electricity',
  'seasonal_rental',
  'guest',
  'motorhome',
  'room_hire',
  'service',
  'other',
] as const

export type PriceCategory = (typeof PRICE_CATEGORIES)[number]

export const PRICE_CATEGORY_KEYS: Record<PriceCategory, string> = {
  membership: 'admin.pricing.categoryMembership',
  harbor_membership: 'admin.pricing.categoryHarborMembership',
  slip_fee: 'admin.pricing.categorySlipFee',
  electricity: 'admin.pricing.categoryElectricity',
  seasonal_rental: 'admin.pricing.categorySeasonalRental',
  guest: 'admin.pricing.categoryGuest',
  motorhome: 'admin.pricing.categoryMotorhome',
  room_hire: 'admin.pricing.categoryRoomHire',
  service: 'admin.pricing.categoryService',
  other: 'admin.pricing.categoryOther',
}

export const PRICE_UNITS = ['once', 'year', 'season', 'day', 'night', 'hour', 'kwh'] as const

export type PriceUnit = (typeof PRICE_UNITS)[number]

export const PRICE_UNIT_KEYS: Record<PriceUnit, string> = {
  once: 'admin.pricing.unitOnce',
  year: 'admin.pricing.unitYear',
  season: 'admin.pricing.unitSeason',
  day: 'admin.pricing.unitDay',
  night: 'admin.pricing.unitNight',
  hour: 'admin.pricing.unitHour',
  kwh: 'admin.pricing.unitKwh',
}

/** The label key for a category, or undefined for one outside the vocabulary. */
export function priceCategoryKey(category: string): string | undefined {
  return (PRICE_CATEGORY_KEYS as Record<string, string>)[category]
}

/** The label key for a unit, or undefined for one outside the vocabulary. */
export function priceUnitKey(unit: string): string | undefined {
  return (PRICE_UNIT_KEYS as Record<string, string>)[unit]
}
