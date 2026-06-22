import { ref } from 'vue'
import { defineStore } from 'pinia'

// The current page label for the top-bar breadcrumb. The active layout
// (PortalView / AdminLayout) sets this from its localized nav definitions
// on every route change, so the global NavBar can render
// "<section> › <page>" without each route having to declare its own title.
export const useBreadcrumbStore = defineStore('breadcrumb', () => {
  const pageLabel = ref('')

  function setPage(label: string) {
    pageLabel.value = label
  }

  return { pageLabel, setPage }
})
