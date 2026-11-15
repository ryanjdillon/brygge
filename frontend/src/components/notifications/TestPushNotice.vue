<script setup lang="ts">
import type { TestPushMessage } from '@/lib/testPushResult'

// The result of "Send testvarsel" (BRY-682), on the member's and the admin's
// notifications pages; build the message with testPushOutcome/testPushFailure.
defineProps<{ message: TestPushMessage }>()
</script>

<template>
  <div
    role="status"
    class="rounded-md p-3 text-sm"
    :class="{
      'bg-green-50 text-green-800': message.kind === 'success',
      'bg-amber-50 text-amber-800': message.kind === 'warning',
      'bg-red-50 text-red-800': message.kind === 'error',
    }"
    data-testid="test-push-result"
  >
    <p v-for="(line, i) in message.lines" :key="i">{{ line }}</p>
  </div>
</template>
