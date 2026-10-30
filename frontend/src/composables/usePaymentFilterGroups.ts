import { ref } from 'vue'
import { useApiClient, unwrap } from '@/lib/apiClient'
import type { components } from '@/types/api'

export type PaymentFilterGroup = components['schemas']['PaymentFilterGroup']
export type PaymentFilterPriceItem = components['schemas']['PaymentFilterPriceItem']
export type PaymentFilterResult = components['schemas']['PaymentFilterGroupResult']

/**
 * The payment overview's filter groups and the club's price items, for the
 * settings page (BRY-522). Plain state, not a query cache: the page edits the
 * list in place (an optimistic reorder that reverts) and re-reads it after
 * every write, and the Payments page reads its own copy on mount.
 *
 * Writes throw ApiError; its `code` is the server's refusal (name_taken,
 * order_mismatch, ...). The caller decides what to show.
 */
export function usePaymentFilterGroups() {
  const client = useApiClient()

  // In display order, disabled groups included, the fallback last.
  const groups = ref<PaymentFilterGroup[]>([])
  const priceItems = ref<PaymentFilterPriceItem[]>([])
  const loading = ref(false)
  // Set instead of an empty list, which would read as "no groups".
  const loadFailed = ref(false)
  const loaded = ref(false)

  // Only the newest read may fill the state.
  let seq = 0

  async function load() {
    const mine = ++seq
    loading.value = true
    try {
      const res = unwrap(await client.GET('/api/v1/admin/payment-filter-groups'))
      if (mine !== seq) return
      groups.value = res.groups
      priceItems.value = res.price_items
      loadFailed.value = false
      loaded.value = true
    } catch {
      if (mine === seq) loadFailed.value = true
    } finally {
      if (mine === seq) loading.value = false
    }
  }

  async function create(body: { name: string; icon: PaymentFilterGroup['icon']; price_item_ids?: string[] }) {
    return unwrap(await client.POST('/api/v1/admin/payment-filter-groups', { body }))
  }

  async function update(
    slug: string,
    body: { name?: string; reset_name?: boolean; icon?: PaymentFilterGroup['icon']; enabled?: boolean },
  ) {
    return unwrap(await client.PATCH('/api/v1/admin/payment-filter-groups/{slug}', { params: { path: { slug } }, body }))
  }

  async function setItems(slug: string, priceItemIds: string[]) {
    return unwrap(
      await client.PUT('/api/v1/admin/payment-filter-groups/{slug}/items', { params: { path: { slug } }, body: { price_item_ids: priceItemIds } }),
    )
  }

  async function reorder(slugs: string[]) {
    return unwrap(await client.PUT('/api/v1/admin/payment-filter-groups/order', { body: { slugs } }))
  }

  async function remove(slug: string) {
    unwrap(await client.DELETE('/api/v1/admin/payment-filter-groups/{slug}', { params: { path: { slug } } }))
  }

  return { groups, priceItems, loading, loadFailed, loaded, load, create, update, setItems, reorder, remove }
}
