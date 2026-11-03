<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { X } from 'lucide-vue-next'
import type { SendMailMessagePayload } from '../api/mailAccounts'

const props = defineProps<{
  open: boolean
  form: SendMailMessagePayload
  sending: boolean
  disabled?: boolean
  notice?: string
}>()
const emit = defineEmits<{
  'update:form': [form: SendMailMessagePayload]
  send: []
  close: []
}>()
const recipientInput = ref<HTMLInputElement | null>(null)
let previousFocus: HTMLElement | null = null

watch(() => props.open, async (open) => {
  if (open) {
    previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
    await nextTick()
    recipientInput.value?.focus()
  } else if (previousFocus?.isConnected) {
    previousFocus.focus()
  }
}, { immediate: true })

function update(field: keyof SendMailMessagePayload, event: Event) {
  const value = (event.target as HTMLInputElement | HTMLTextAreaElement).value
  emit('update:form', { ...props.form, [field]: field === 'body' ? value : value.trim() })
}

function trapFocus(event: KeyboardEvent) {
  const dialog = event.currentTarget as HTMLElement
  const items = [...dialog.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), textarea:not(:disabled)')]
  const first = items[0], last = items[items.length - 1]
  if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus() }
  else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
}
</script>

<template>
  <Teleport to="body">
    <div v-if="open" class="mail-send-mask" @keydown.esc.stop.prevent="emit('close')">
      <form class="mail-send-modal overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-dark-700 dark:bg-dark-900"
        role="dialog" aria-modal="true" aria-label="发送邮件" :aria-busy="sending"
        @keydown.tab="trapFocus" @submit.prevent="!sending && !disabled && emit('send')">
        <div class="shrink-0 flex items-center justify-between border-b border-gray-200 px-6 py-4 dark:border-dark-700">
          <h3 class="text-base font-bold text-gray-900 dark:text-white">发送邮件</h3>
          <button class="modal-close-button" type="button" aria-label="关闭发件框" @click="emit('close')"><X class="h-5 w-5" /></button>
        </div>
        <div class="mail-send-scroll-body grid gap-4 p-6">
          <p v-if="notice" class="text-sm text-amber-700 dark:text-amber-400" role="status">{{ notice }}</p>
          <label><span class="input-label">昵称</span>
            <input :value="form.nickname" class="input" name="nickname" placeholder="收件人看到的发件人昵称（可选）" :disabled="sending" @input="update('nickname', $event)" />
          </label>
          <label><span class="input-label">收件人 *</span>
            <input ref="recipientInput" :value="form.recipient" class="input" name="recipient" aria-required="true" placeholder="例: target@example.com" :disabled="sending" @input="update('recipient', $event)" />
          </label>
          <label><span class="input-label">主题</span>
            <input :value="form.subject" class="input" name="subject" placeholder="邮件主题" :disabled="sending" @input="update('subject', $event)" />
          </label>
          <label><span class="input-label">正文</span>
            <textarea :value="form.body" class="input mail-send-body-textarea" name="body" placeholder="邮件正文" :disabled="sending" @input="update('body', $event)"></textarea>
          </label>
        </div>
        <div class="shrink-0 flex justify-end gap-3 border-t border-gray-200 px-6 py-4 dark:border-dark-700">
          <button class="btn btn-secondary" type="button" @click="emit('close')">取消</button>
          <button class="btn btn-primary" type="submit" :disabled="sending || disabled">{{ sending ? '发送中...' : '发送' }}</button>
        </div>
      </form>
    </div>
  </Teleport>
</template>

<style scoped>
.mail-send-mask {
  position: fixed;
  inset: 0;
  z-index: 1000;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 1rem;
  overflow: hidden;
  background: rgb(0 0 0 / 0.45);
  -webkit-backdrop-filter: blur(4px);
  backdrop-filter: blur(4px);
}
.mail-send-modal {
  display: flex;
  flex-direction: column;
  width: min(37.5rem, calc(100vw - 2rem));
  max-height: calc(100dvh - 2rem);
}
.mail-send-scroll-body { min-height: 0; overflow-y: auto; }
.mail-send-body-textarea { min-height: 7.25rem; resize: vertical; }
.modal-close-button { display: inline-flex; align-items: center; justify-content: center; border-radius: .5rem; padding: .25rem; color: #64748b; }
.modal-close-button:hover { background: rgb(148 163 184 / .15); }
@media (max-width: 640px) {
  .mail-send-mask { padding: .5rem; }
  .mail-send-modal { width: calc(100vw - 1rem); max-height: calc(100dvh - 1rem); }
  .mail-send-scroll-body { padding: 1rem; gap: .75rem; }
}
</style>
