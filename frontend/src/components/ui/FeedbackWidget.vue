<script setup lang="ts">
import { ref, computed, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessageSquare, Bug, Lightbulb, Camera, X, Send, Loader2, CheckCircle, GripHorizontal } from 'lucide-vue-next'
import { useApi } from '@/composables/useApi'

const { t } = useI18n()
const { fetchApi } = useApi()

const open = ref(false)
const type = ref<'bug' | 'feature'>('bug')
const title = ref('')
const description = ref('')
const pageURL = ref(window.location.href)
const screenshot = ref<string | null>(null)
const capturing = ref(false)
const submitting = ref(false)
const submitted = ref(false)
const error = ref('')

const panelHeight = ref(0)
const panelRef = ref<HTMLElement | null>(null)

const canSubmit = computed(() => description.value.trim().length > 0 && !submitting.value)

function toggle() {
  open.value = !open.value
  if (open.value) {
    pageURL.value = window.location.href
    submitted.value = false
    error.value = ''
    panelHeight.value = 0
  }
}

function close() {
  open.value = false
}

function reset() {
  type.value = 'bug'
  title.value = ''
  description.value = ''
  screenshot.value = null
  error.value = ''
  submitted.value = false
  panelHeight.value = 0
}

// Resize handle — drag up from top-right corner to expand panel upward.
let resizeStartY = 0
let resizeStartH = 0

function startResize(e: MouseEvent) {
  e.preventDefault()
  resizeStartY = e.clientY
  resizeStartH = panelRef.value?.offsetHeight ?? 420
  document.addEventListener('mousemove', onResizeMove)
  document.addEventListener('mouseup', stopResize)
}

function onResizeMove(e: MouseEvent) {
  const dy = e.clientY - resizeStartY
  panelHeight.value = Math.max(300, resizeStartH - dy)
}

function stopResize() {
  document.removeEventListener('mousemove', onResizeMove)
  document.removeEventListener('mouseup', stopResize)
}

onBeforeUnmount(() => {
  document.removeEventListener('mousemove', onResizeMove)
  document.removeEventListener('mouseup', stopResize)
})

async function captureScreenshot() {
  capturing.value = true
  try {
    const html2canvas = (await import('html2canvas')).default
    const canvas = await html2canvas(document.body, {
      useCORS: true,
      allowTaint: false,
      scale: Math.min(window.devicePixelRatio, 1.5),
      logging: false,
      ignoreElements: (el: Element) => el.classList.contains('feedback-widget'),
    })
    screenshot.value = canvas.toDataURL('image/png')
  } catch {
    // not all pages are capturable; skip silently
  } finally {
    capturing.value = false
  }
}

function removeScreenshot() {
  screenshot.value = null
}

async function submit() {
  if (!canSubmit.value) return
  submitting.value = true
  error.value = ''
  try {
    await fetchApi('/api/v1/feedback', {
      method: 'POST',
      body: JSON.stringify({
        type: type.value,
        title: title.value.trim() || undefined,
        description: description.value.trim(),
        page_url: pageURL.value,
        screenshot: screenshot.value ?? undefined,
      }),
    })
    submitted.value = true
    setTimeout(() => {
      open.value = false
      reset()
    }, 2000)
  } catch (e: any) {
    error.value = e?.message ?? t('feedback.errorGeneric')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="feedback-widget">
    <button
      class="feedback-trigger"
      :aria-label="t('feedback.triggerLabel')"
      @click="toggle"
    >
      <MessageSquare class="h-5 w-5" aria-hidden="true" />
    </button>

    <Transition name="feedback-panel">
      <div
        v-if="open"
        ref="panelRef"
        class="feedback-panel"
        :style="panelHeight ? { height: panelHeight + 'px' } : {}"
        role="dialog"
        :aria-label="t('feedback.panelLabel')"
      >
        <!-- resize handle — top-right corner, drag up to expand -->
        <div class="resize-handle" @mousedown="startResize">
          <GripHorizontal class="h-3 w-3 text-gray-400" aria-hidden="true" />
        </div>

        <div class="feedback-header">
          <span class="text-sm font-semibold text-gray-900">{{ t('feedback.title') }}</span>
          <button class="text-gray-400 hover:text-gray-600 transition-colors" :aria-label="t('common.close')" @click="close">
            <X class="h-4 w-4" aria-hidden="true" />
          </button>
        </div>

        <div v-if="submitted" class="feedback-success">
          <CheckCircle class="h-8 w-8 text-green-500 mx-auto mb-2" aria-hidden="true" />
          <p class="text-sm text-gray-700 font-medium text-center">{{ t('feedback.successMessage') }}</p>
        </div>

        <template v-else>
          <div class="feedback-body">
            <div class="type-toggle">
              <button
                :class="['type-btn', type === 'bug' && 'active']"
                @click="type = 'bug'"
              >
                <Bug class="h-3.5 w-3.5" aria-hidden="true" />
                {{ t('feedback.typeBug') }}
              </button>
              <button
                :class="['type-btn', type === 'feature' && 'active']"
                @click="type = 'feature'"
              >
                <Lightbulb class="h-3.5 w-3.5" aria-hidden="true" />
                {{ t('feedback.typeFeature') }}
              </button>
            </div>

            <input
              v-model="title"
              type="text"
              class="feedback-input"
              :placeholder="t('feedback.titlePlaceholder')"
            />

            <textarea
              v-model="description"
              class="feedback-textarea"
              :placeholder="t('feedback.descriptionPlaceholder')"
              rows="4"
            />

            <div class="screenshot-row">
              <button
                v-if="!screenshot"
                class="screenshot-btn"
                :disabled="capturing"
                @click="captureScreenshot"
              >
                <Loader2 v-if="capturing" class="h-3.5 w-3.5 animate-spin" aria-hidden="true" />
                <Camera v-else class="h-3.5 w-3.5" aria-hidden="true" />
                {{ capturing ? t('feedback.capturing') : t('feedback.captureScreenshot') }}
              </button>
              <div v-else class="screenshot-preview-wrap">
                <img :src="screenshot" class="screenshot-preview" :alt="t('feedback.screenshotAlt')" />
                <button class="screenshot-remove" :aria-label="t('feedback.removeScreenshot')" @click="removeScreenshot">
                  <X class="h-3 w-3" aria-hidden="true" />
                </button>
              </div>
            </div>

            <p v-if="error" class="text-xs text-red-600 mt-1">{{ error }}</p>
          </div>

          <div class="feedback-footer">
            <button
              class="submit-btn"
              :disabled="!canSubmit"
              @click="submit"
            >
              <Loader2 v-if="submitting" class="h-4 w-4 animate-spin" aria-hidden="true" />
              <Send v-else class="h-4 w-4" aria-hidden="true" />
              {{ submitting ? t('common.saving') : t('feedback.submit') }}
            </button>
          </div>
        </template>
      </div>
    </Transition>
  </div>
</template>

<style scoped>
.feedback-widget {
  position: fixed;
  bottom: 1.5rem;
  right: 1.5rem;
  z-index: 9999;
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 0.5rem;
}

.feedback-trigger {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 2.75rem;
  height: 2.75rem;
  border-radius: 9999px;
  background-color: #1d4ed8;
  color: #fff;
  border: none;
  cursor: pointer;
  box-shadow: 0 4px 14px rgba(0,0,0,0.18);
  transition: background-color 0.15s, transform 0.15s;
}
.feedback-trigger:hover {
  background-color: #1e40af;
  transform: scale(1.06);
}

.feedback-panel {
  position: relative;
  width: 320px;
  min-height: 300px;
  display: flex;
  flex-direction: column;
  border-radius: 0.75rem;
  background: #fff;
  box-shadow: 0 8px 32px rgba(0,0,0,0.14);
  border: 1px solid #e5e7eb;
  overflow: hidden;
}

.resize-handle {
  position: absolute;
  top: 0;
  right: 0;
  width: 28px;
  height: 20px;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: n-resize;
  z-index: 1;
  border-bottom-left-radius: 0.375rem;
  background: #f9fafb;
  border-bottom: 1px solid #f3f4f6;
  border-left: 1px solid #f3f4f6;
  border-top-right-radius: 0.75rem;
}
.resize-handle:hover {
  background: #f3f4f6;
}

.feedback-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.75rem 1rem;
  padding-right: 2.25rem;
  border-bottom: 1px solid #f3f4f6;
  flex-shrink: 0;
}

.feedback-success {
  padding: 2rem 1rem;
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
}

.feedback-body {
  padding: 0.75rem 1rem;
  display: flex;
  flex-direction: column;
  gap: 0.625rem;
  flex: 1;
  overflow-y: auto;
  min-height: 0;
}

.feedback-footer {
  padding: 0.625rem 1rem 0.875rem;
  flex-shrink: 0;
}

.type-toggle {
  display: flex;
  gap: 0.375rem;
}

.type-btn {
  flex: 1;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 0.3rem;
  padding: 0.375rem 0.5rem;
  border-radius: 0.5rem;
  border: 1px solid #e5e7eb;
  background: #f9fafb;
  color: #6b7280;
  font-size: 0.75rem;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.12s;
}
.type-btn.active {
  border-color: #1d4ed8;
  background: #eff6ff;
  color: #1d4ed8;
}
.type-btn:hover:not(.active) {
  background: #f3f4f6;
  color: #374151;
}

.feedback-input {
  width: 100%;
  padding: 0.4rem 0.625rem;
  border: 1px solid #e5e7eb;
  border-radius: 0.5rem;
  font-size: 0.8125rem;
  color: #111827;
  outline: none;
  box-sizing: border-box;
  transition: border-color 0.12s;
  flex-shrink: 0;
}
.feedback-input:focus {
  border-color: #1d4ed8;
}

.feedback-textarea {
  width: 100%;
  padding: 0.4rem 0.625rem;
  border: 1px solid #e5e7eb;
  border-radius: 0.5rem;
  font-size: 0.8125rem;
  color: #111827;
  outline: none;
  resize: none;
  min-height: 80px;
  flex: 1;
  box-sizing: border-box;
  font-family: inherit;
  transition: border-color 0.12s;
}
.feedback-textarea:focus {
  border-color: #1d4ed8;
}

.screenshot-row {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-shrink: 0;
}

.screenshot-btn {
  display: inline-flex;
  align-items: center;
  gap: 0.3rem;
  padding: 0.3rem 0.625rem;
  border: 1px solid #e5e7eb;
  border-radius: 0.5rem;
  background: #f9fafb;
  color: #6b7280;
  font-size: 0.75rem;
  cursor: pointer;
  transition: all 0.12s;
}
.screenshot-btn:hover:not(:disabled) {
  background: #f3f4f6;
  color: #374151;
}
.screenshot-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.screenshot-preview-wrap {
  position: relative;
  display: inline-block;
}
.screenshot-preview {
  height: 48px;
  width: auto;
  border-radius: 0.375rem;
  border: 1px solid #e5e7eb;
  object-fit: cover;
}
.screenshot-remove {
  position: absolute;
  top: -6px;
  right: -6px;
  width: 16px;
  height: 16px;
  border-radius: 9999px;
  background: #ef4444;
  color: #fff;
  border: none;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 0;
}

.submit-btn {
  width: 100%;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 0.4rem;
  padding: 0.5rem 1rem;
  border-radius: 0.5rem;
  background: #1d4ed8;
  color: #fff;
  font-size: 0.8125rem;
  font-weight: 500;
  border: none;
  cursor: pointer;
  transition: background-color 0.12s;
}
.submit-btn:hover:not(:disabled) {
  background: #1e40af;
}
.submit-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.feedback-panel-enter-active,
.feedback-panel-leave-active {
  transition: opacity 0.15s, transform 0.15s;
}
.feedback-panel-enter-from,
.feedback-panel-leave-to {
  opacity: 0;
  transform: translateY(8px) scale(0.97);
}
</style>
