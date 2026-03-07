import { defineStore } from 'pinia'
import { ref, computed } from 'vue'

export interface CartItem {
  id: string
  type: 'product' | 'price_item'
  name: string
  unitPrice: number
  quantity: number
  imageUrl?: string
  variantId?: string
  size?: string
  color?: string
}

export const useCartStore = defineStore('cart', () => {
  const items = ref<CartItem[]>(loadFromStorage())

  const totalItems = computed(() =>
    items.value.reduce((sum, item) => sum + item.quantity, 0),
  )

  const totalAmount = computed(() =>
    items.value.reduce((sum, item) => sum + item.unitPrice * item.quantity, 0),
  )

  function addItem(item: Omit<CartItem, 'quantity'>, quantity = 1) {
    const existing = items.value.find(
      (i) => i.id === item.id && i.type === item.type && (i.variantId ?? '') === (item.variantId ?? ''),
    )
    if (existing) {
      existing.quantity += quantity
    } else {
      items.value.push({ ...item, quantity })
    }
    persist()
  }

  function updateQuantity(id: string, quantity: number, variantId?: string) {
    const item = items.value.find(
      (i) => i.id === id && (i.variantId ?? '') === (variantId ?? ''),
    )
    if (!item) return
    if (quantity <= 0) {
      removeItem(id, variantId)
      return
    }
    item.quantity = quantity
    persist()
  }

  function removeItem(id: string, variantId?: string) {
    items.value = items.value.filter(
      (i) => !(i.id === id && (i.variantId ?? '') === (variantId ?? '')),
    )
    persist()
  }

  function clear() {
    items.value = []
    persist()
  }

  function persist() {
    try {
      localStorage.setItem('brygge_cart', JSON.stringify(items.value))
    } catch {
      // ignore storage errors
    }
  }

  function loadFromStorage(): CartItem[] {
    try {
      const raw = localStorage.getItem('brygge_cart')
      return raw ? JSON.parse(raw) : []
    } catch {
      return []
    }
  }

  return { items, totalItems, totalAmount, addItem, updateQuantity, removeItem, clear }
})
