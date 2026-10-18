<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { getAuthToken, watchAuthSession } from './api/session'
import ConfirmDialog from './ConfirmDialog.vue'
import Toast from './Toast.vue'
import { useAppStore } from './stores/app'

const appStore = useAppStore()
const route = useRoute()
const router = useRouter()
const sessionViewKey = ref(getAuthToken())
const stopWatchingAuth = watchAuthSession(() => {
  sessionViewKey.value = getAuthToken()
  if (!sessionViewKey.value && !route.meta.public) void router.replace('/login')
})
onBeforeUnmount(stopWatchingAuth)

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
  <RouterView v-if="route.meta.public || sessionViewKey" :key="route.path.startsWith('/admin') ? sessionViewKey : ''" />
  <ConfirmDialog />
  <Toast />
</template>
