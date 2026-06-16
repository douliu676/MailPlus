<script setup lang="ts">
import { watch } from 'vue'
import ConfirmDialog from './ConfirmDialog.vue'
import Toast from './Toast.vue'
import { useAppStore } from './stores/app'

const appStore = useAppStore()

function updateFavicon(logoUrl: string) {
  let link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
  if (!link) {
    link = document.createElement('link')
    link.rel = 'icon'
    document.head.appendChild(link)
  }

  link.type = logoUrl.startsWith('data:image/svg') || logoUrl.endsWith('.svg') ? 'image/svg+xml' : 'image/png'
  link.href = logoUrl
}

watch(
  () => appStore.siteLogo.value,
  (logo) => {
    if (logo) {
      updateFavicon(logo)
    }
  },
  { immediate: true }
)
</script>

<template>
  <RouterView />
  <ConfirmDialog />
  <Toast />
</template>
