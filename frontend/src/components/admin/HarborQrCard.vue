<script setup lang="ts">
import { computed } from 'vue'
import { type CardsPerPage } from '@/lib/qrSheet'

const props = defineProps<{
  label: string
  price: string
  qrSrc: string
  per: CardsPerPage
  shortUrl?: string
  // Already filtered and ordered by the caller: the selected languages other
  // than the club's, and only those the target has a name for.
  translations?: string[]
}>()

const translationLine = computed(() => (props.translations ?? []).join(' · '))

// Printed without the scheme: shorter to read and type, and browsers add it.
const shortAddress = computed(() => (props.shortUrl ?? '').replace(/^https?:\/\//, ''))
// Each segment keeps its trailing "/", so a <wbr> between segments offers a
// line break right after the slash.
const shortSegments = computed(() => shortAddress.value.split(/(?<=\/)/))
</script>

<template>
  <article class="qr-card" :class="per === 2 ? 'qr-card--large' : ''" data-testid="qr-card">
    <h2 class="qr-card__title">{{ label }}</h2>
    <p v-if="translationLine" class="qr-card__translations" data-testid="qr-card-translations">
      {{ translationLine }}
    </p>
    <p class="qr-card__price" data-testid="qr-card-price">{{ price }}</p>
    <img v-if="qrSrc" class="qr-card__code" :src="qrSrc" :alt="label" />
    <p v-if="shortAddress" class="qr-card__short-url" data-testid="qr-card-short-url">
      <template v-for="(seg, i) in shortSegments" :key="i"><wbr v-if="i > 0" />{{ seg }}</template>
    </p>
  </article>
</template>

<style scoped>
/* The card fills its cell of the page grid, so its size follows the
   cards-per-page choice and the height the header leaves. */
.qr-card {
  box-sizing: border-box;
  width: 100%;
  height: 100%;
  min-height: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: 5mm 5mm 4mm;
  border: 0.6mm solid #1e293b;
  border-radius: 4mm;
  background: #fff;
  color: #0f172a;
  overflow: hidden;
  text-align: center;
}

.qr-card__title {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  overflow: hidden;
  margin: 0;
  font-size: 15pt;
  font-weight: 700;
  line-height: 1.15;
  letter-spacing: -0.01em;
}

/* Clamped so a long list of names can't take the space the QR needs. */
.qr-card__translations {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  overflow: hidden;
  flex-shrink: 0;
  margin: 0.5mm 0 0;
  font-size: 9pt;
  line-height: 1.2;
  color: #64748b;
}

.qr-card__price {
  margin: 1mm 0 2mm;
  font-size: 12pt;
  font-weight: 500;
  color: #334155;
  font-variant-numeric: tabular-nums;
}

/* The QR takes whatever height the title and price leave; object-fit keeps
   it square and centred. */
.qr-card__code {
  flex: 1 1 0;
  min-height: 0;
  width: 100%;
  object-fit: contain;
}

/* Allowed to wrap only after a "/", so a long domain on a small card breaks
   at a readable point instead of mid-word. */
.qr-card__short-url {
  flex-shrink: 0;
  margin: 1.5mm 0 0;
  max-width: 100%;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, 'Liberation Mono', monospace;
  font-size: 9pt;
  line-height: 1.2;
  color: #1e293b;
  overflow-wrap: normal;
  word-break: keep-all;
}

.qr-card--large {
  padding: 8mm 8mm 6mm;
}

.qr-card--large .qr-card__title {
  font-size: 28pt;
}

.qr-card--large .qr-card__translations {
  margin-top: 1mm;
  font-size: 14pt;
}

.qr-card--large .qr-card__price {
  margin: 2mm 0 4mm;
  font-size: 18pt;
}

.qr-card--large .qr-card__short-url {
  margin-top: 3mm;
  font-size: 14pt;
}
</style>
