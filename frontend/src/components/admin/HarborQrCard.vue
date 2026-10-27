<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
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
  // Set by the print view's fit measurement: even at the smallest text size
  // the QR would fall below its minimum.
  overflow?: boolean
}>()

const { t } = useI18n()

const translationLine = computed(() => (props.translations ?? []).join(' · '))

// Printed without the scheme: shorter to read and type, and browsers add it.
const shortAddress = computed(() => (props.shortUrl ?? '').replace(/^https?:\/\//, ''))
// Each segment keeps its trailing "/", so a <wbr> between segments offers a
// line break right after the slash.
const shortSegments = computed(() => shortAddress.value.split(/(?<=\/)/))
</script>

<template>
  <article
    class="qr-card"
    :class="[per === 2 ? 'qr-card--large' : '', overflow ? 'qr-card--overflow' : '']"
    data-testid="qr-card"
  >
    <div class="qr-card__text" data-fit="text">
      <h2 class="qr-card__title">{{ label }}</h2>
      <p v-if="translationLine" class="qr-card__translations" data-testid="qr-card-translations">
        {{ translationLine }}
      </p>
      <p class="qr-card__price" data-testid="qr-card-price">{{ price }}</p>
    </div>
    <img v-if="qrSrc" class="qr-card__code" :src="qrSrc" :alt="label" />
    <p v-if="shortAddress" class="qr-card__short-url" data-fit="fixed" data-testid="qr-card-short-url">
      <template v-for="(seg, i) in shortSegments" :key="i"><wbr v-if="i > 0" />{{ seg }}</template>
    </p>
    <p v-if="overflow" class="qr-card__overflow" data-testid="qr-card-overflow">
      {{ t('harborTargets.print.overflowNote') }}
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
  position: relative;
  overflow: hidden;
  text-align: center;
}

/* Title, translations and price scale together with --fit, which the print
   view sets per card so the QR keeps its minimum size and long text shrinks
   instead of being cut off.
   A flex column so the children's margins count in its measured height. */
.qr-card__text {
  flex: none;
  display: flex;
  flex-direction: column;
  align-items: center;
  max-width: 100%;
}

.qr-card__title {
  margin: 0;
  max-width: 100%;
  overflow-wrap: break-word;
  font-size: calc(15pt * var(--fit, 1));
  font-weight: 700;
  line-height: 1.15;
  letter-spacing: -0.01em;
}

.qr-card__translations {
  margin: 0.5mm 0 0;
  max-width: 100%;
  overflow-wrap: break-word;
  font-size: calc(9pt * var(--fit, 1));
  line-height: 1.2;
  color: #64748b;
}

.qr-card__price {
  margin: 1mm 0 2mm;
  font-size: calc(12pt * var(--fit, 1));
  font-weight: 500;
  color: #334155;
  font-variant-numeric: tabular-nums;
}

/* The QR takes whatever height the text leaves; object-fit keeps it square
   and centred. Its minimum size comes from the fit measurement, not from a
   min-height that would push the short address out of the card. */
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
  font-size: calc(28pt * var(--fit, 1));
}

.qr-card--large .qr-card__translations {
  margin-top: 1mm;
  font-size: calc(14pt * var(--fit, 1));
}

.qr-card--large .qr-card__price {
  margin: 2mm 0 4mm;
  font-size: calc(18pt * var(--fit, 1));
}

.qr-card--large .qr-card__short-url {
  margin-top: 3mm;
  font-size: 14pt;
}

/* Screen-only warning: an outline and an overlaid note take no layout space,
   so they don't change what is measured or printed. */
.qr-card--overflow {
  outline: 1mm solid #dc2626;
  outline-offset: 1mm;
}

.qr-card__overflow {
  position: absolute;
  left: 2mm;
  right: 2mm;
  bottom: 2mm;
  margin: 0;
  padding: 1.5mm 2mm;
  border-radius: 1.5mm;
  background: #fef2f2;
  color: #b91c1c;
  font-size: 9pt;
  line-height: 1.25;
}

@media print {
  .qr-card--overflow {
    outline: none;
  }

  .qr-card__overflow {
    display: none;
  }
}
</style>
