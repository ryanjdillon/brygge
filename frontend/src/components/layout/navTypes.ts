import type { Component } from 'vue'

// Shared shape for sidebar navigation, used by both the admin and the
// member portal layouts so the two sidebars render identically.
export interface NavItem {
  to: string
  icon: Component
  label: string
  roles?: string[]
  feature?: 'bookings' | 'projects' | 'calendar' | 'commerce' | 'accounting'
  badge?: number
}

export interface NavGroup {
  titleKey?: string
  items: NavItem[]
}
