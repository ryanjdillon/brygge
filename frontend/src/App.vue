<script setup lang="ts">
import { RouterView } from 'vue-router'
import NavBar from '@/components/layout/NavBar.vue'
import Footer from '@/components/layout/Footer.vue'
</script>

<template>
  <div class="flex min-h-screen flex-col">
    <NavBar />
    <main class="flex-1">
      <RouterView />
    </main>
    <Footer />
  </div>
</template>
