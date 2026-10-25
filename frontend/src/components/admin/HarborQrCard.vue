<script setup lang="ts">
import { computed } from 'vue'
import { cellSizeMm, type CardsPerPage } from '@/lib/qrSheet'

const props = defineProps<{
  label: string
  price: string
  qrSrc: string
  per: CardsPerPage
}>()

const size = computed(() => {
  const { width, height } = cellSizeMm(props.per)
  return { width: `${width}mm`, height: `${height}mm` }
})
</script>

<template>
  <article class="qr-card" :class="per === 2 ? 'qr-card--large' : ''" :style="size" data-testid="qr-card">
    <h2 class="qr-card__title">{{ label }}</h2>
    <p class="qr-card__price" data-testid="qr-card-price">{{ price }}</p>
    <img v-if="qrSrc" class="qr-card__code" :src="qrSrc" :alt="label" />
  </article>
</template>

<style scoped>
.qr-card {
  box-sizing: border-box;
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

.qr-card--large {
  padding: 8mm 8mm 6mm;
}

.qr-card--large .qr-card__title {
  font-size: 28pt;
}

.qr-card--large .qr-card__price {
  margin: 2mm 0 4mm;
  font-size: 18pt;
}
</style>
