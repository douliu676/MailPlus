<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Info, TriangleAlert, XCircle } from 'lucide-vue-next'
import { useAppStore, type ConfirmTone } from './stores/app'

const appStore = useAppStore()
const dialog = computed(() => appStore.confirmDialog.value)
const confirmButtonRef = ref<HTMLButtonElement | null>(null)

function iconByTone(tone: ConfirmTone) {
  if (tone === 'info') return Info
  if (tone === 'warning') return TriangleAlert
  return XCircle
}

function iconClass(tone: ConfirmTone) {
  if (tone === 'info') return 'bg-blue-50 text-blue-600 dark:bg-blue-950/40 dark:text-blue-300'
  if (tone === 'warning') return 'bg-amber-50 text-amber-600 dark:bg-amber-950/40 dark:text-amber-300'
  return 'bg-red-50 text-red-600 dark:bg-red-950/40 dark:text-red-300'
}

function confirmClass(tone: ConfirmTone) {
  if (tone === 'info') return 'bg-blue-600 text-white hover:bg-blue-500 focus:ring-blue-500/40'
  if (tone === 'warning') return 'bg-amber-500 text-white hover:bg-amber-400 focus:ring-amber-500/40'
  return 'bg-red-600 text-white hover:bg-red-500 focus:ring-red-500/40'
}

function handleKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && dialog.value) {
    appStore.cancelConfirm()
  }
}

watch(dialog, async (value) => {
  if (!value) return
  await nextTick()
  confirmButtonRef.value?.focus()
})

onMounted(() => {
  window.addEventListener('keydown', handleKeydown)
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', handleKeydown)
})
</script>

<template>
  <Teleport to="body">
    <Transition
      enter-active-class="transition duration-150 ease-out"
      enter-from-class="opacity-0"
      enter-to-class="opacity-100"
      leave-active-class="transition duration-100 ease-in"
      leave-from-class="opacity-100"
      leave-to-class="opacity-0"
    >
      <div
        v-if="dialog"
        class="fixed inset-0 z-[10000] flex items-center justify-center bg-black/45 p-4 backdrop-blur-sm"
        role="presentation"
        @click.self="appStore.cancelConfirm"
      >
        <section
          class="w-full max-w-md overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-2xl shadow-black/15 dark:border-dark-700 dark:bg-dark-900 dark:shadow-black/40"
          role="dialog"
          aria-modal="true"
          :aria-labelledby="`${dialog.id}-title`"
          :aria-describedby="`${dialog.id}-message`"
        >
          <div class="flex gap-4 px-6 py-5">
            <div class="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl" :class="iconClass(dialog.tone)">
              <component :is="iconByTone(dialog.tone)" class="h-5 w-5" />
            </div>
            <div class="min-w-0 flex-1">
              <h3 :id="`${dialog.id}-title`" class="text-base font-bold text-gray-900 dark:text-white">{{ dialog.title }}</h3>
              <p :id="`${dialog.id}-message`" class="mt-2 text-sm leading-6 text-gray-600 dark:text-dark-300">{{ dialog.message }}</p>
              <p v-if="dialog.description" class="mt-2 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ dialog.description }}</p>
            </div>
          </div>

          <div class="flex justify-end gap-2 border-t border-gray-100 bg-gray-50 px-6 py-4 dark:border-dark-700 dark:bg-dark-800/70">
            <button class="btn btn-secondary" type="button" @click="appStore.cancelConfirm">{{ dialog.cancelText }}</button>
            <button ref="confirmButtonRef" class="btn" :class="confirmClass(dialog.tone)" type="button" @click="appStore.confirmAction">
              {{ dialog.confirmText }}
            </button>
          </div>
        </section>
      </div>
    </Transition>
  </Teleport>
</template>
