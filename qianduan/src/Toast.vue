<script setup lang="ts">
import { computed } from 'vue'
import { CheckCircle2, Info, TriangleAlert, X, XCircle } from 'lucide-vue-next'
import { useAppStore, type ToastSegmentTone, type ToastType } from './stores/app'

const appStore = useAppStore()
const toasts = computed(() => appStore.toasts.value)

function iconByType(type: ToastType) {
  if (type === 'success') return CheckCircle2
  if (type === 'error') return XCircle
  if (type === 'warning') return TriangleAlert
  return Info
}

function borderByType(type: ToastType) {
  const colors = {
    success: 'border-green-500',
    error: 'border-red-500',
    warning: 'border-yellow-500',
    info: 'border-blue-500',
  }
  return colors[type]
}

function iconColorByType(type: ToastType) {
  const colors = {
    success: 'text-green-500',
    error: 'text-red-500',
    warning: 'text-yellow-500',
    info: 'text-blue-500',
  }
  return colors[type]
}

function progressByType(type: ToastType) {
  const colors = {
    success: 'bg-green-500',
    error: 'bg-red-500',
    warning: 'bg-yellow-500',
    info: 'bg-blue-500',
  }
  return colors[type]
}

function segmentClass(tone: ToastSegmentTone = 'normal') {
  if (tone === 'success') return 'text-green-500 dark:text-green-400'
  if (tone === 'error') return 'text-red-500 dark:text-red-400'
  return ''
}
</script>

<template>
  <Teleport to="body">
    <div
      class="pointer-events-none fixed right-4 top-4 z-[9999] space-y-3"
      aria-live="polite"
      aria-atomic="true"
    >
      <TransitionGroup
        enter-active-class="transition ease-out duration-300"
        enter-from-class="translate-x-full opacity-0"
        enter-to-class="translate-x-0 opacity-100"
        leave-active-class="transition ease-in duration-200"
        leave-from-class="translate-x-0 opacity-100"
        leave-to-class="translate-x-full opacity-0"
      >
        <div
          v-for="toast in toasts"
          :key="toast.id"
          class="pointer-events-auto w-[320px] overflow-hidden rounded-lg border-l-4 bg-white shadow-[0_10px_30px_rgba(15,23,42,0.12)] dark:bg-[#1e293b] dark:shadow-lg"
          :class="borderByType(toast.type)"
        >
          <div class="p-4">
            <div class="flex items-start gap-3">
            <component :is="iconByType(toast.type)" class="mt-0.5 h-5 w-5 shrink-0" :class="iconColorByType(toast.type)" />
            <div class="min-w-0 flex-1">
              <p v-if="toast.title" class="text-sm font-medium text-gray-900 dark:text-white">{{ toast.title }}</p>
              <p
                class="text-sm font-medium leading-6"
                :class="toast.title ? 'mt-1 text-gray-600 dark:text-gray-300' : 'text-gray-900 dark:text-white'"
              >
                <template v-if="toast.segments?.length">
                  <span v-for="(segment, index) in toast.segments" :key="`${toast.id}-${index}`" :class="segmentClass(segment.tone)">
                    {{ segment.text }}
                  </span>
                </template>
                <template v-else>{{ toast.message }}</template>
              </p>
            </div>
            <button
              class="-m-1 rounded p-1 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-white/10 dark:hover:text-gray-200"
              type="button"
              @click="appStore.hideToast(toast.id)"
            >
              <X class="h-4 w-4" />
            </button>
            </div>
          </div>
          <div v-if="toast.duration" class="h-1 bg-gray-100 dark:bg-[#334155]">
            <div class="toast-progress h-full" :class="progressByType(toast.type)" :style="{ animationDuration: `${toast.duration}ms` }"></div>
          </div>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>
