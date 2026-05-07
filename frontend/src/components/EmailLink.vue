<script setup lang="ts">
import { computed } from 'vue'

// Anti-scrape email link. The email is split into user/domain parts at
// render time, joined with a `&#64;` HTML entity in the visible text,
// and the actual `mailto:` URL is only assembled when the user clicks
// — so naive scrapers parsing static HTML don't get a literal
// `name@domain` string nor a `mailto:` href to harvest.
//
// Renders a <button> rather than an <a> on purpose: anchors with
// mailto: hrefs are the canonical scraper target.
const props = defineProps<{
  address: string
  className?: string
}>()

const parts = computed(() => {
  const at = props.address.indexOf('@')
  if (at <= 0) return { user: props.address, domain: '' }
  return { user: props.address.slice(0, at), domain: props.address.slice(at + 1) }
})

function open() {
  const { user, domain } = parts.value
  if (!domain) return
  // Build the mailto in two halves so the literal "mailto:" + email
  // pair never appears together in any one source location either.
  window.location.href = ['mailto', ':', user, String.fromCharCode(64), domain].join('')
}
</script>

<template>
  <button
    type="button"
    :class="className || 'text-blue-600 hover:underline'"
    @click="open"
  >
    <span>{{ parts.user }}</span><span v-html="'&#64;'" /><span>{{ parts.domain }}</span>
  </button>
</template>
