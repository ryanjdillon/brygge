<script setup lang="ts">
import { RouterView } from 'vue-router'
import NavBar from '@/components/layout/NavBar.vue'
import Footer from '@/components/layout/Footer.vue'
</script>

<template>
  <div class="flex min-h-screen flex-col">
    <a href="#main-content" class="skip-nav">Hopp til hovedinnhold</a>
    <NavBar />
    <main id="main-content" class="flex-1">
      <RouterView />
    </main>
    <Footer />
  </div>
</template>
