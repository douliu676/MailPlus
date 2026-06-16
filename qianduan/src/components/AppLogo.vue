<script setup lang="ts">
import { computed, ref, watch } from 'vue'

const props = withDefaults(
  defineProps<{
    src: string
    alt?: string
  }>(),
  {
    alt: 'Logo',
  }
)

const loadedSrc = ref('')
let loadVersion = 0

const isReady = computed(() => loadedSrc.value === props.src)

watch(
  () => props.src,
  (src) => {
    const version = ++loadVersion

    if (!src) {
      loadedSrc.value = ''
      return
    }

    if (src.startsWith('data:image/')) {
      loadedSrc.value = src
      return
    }

    const image = new Image()
    image.decoding = 'async'
    image.onload = async () => {
      try {
        await image.decode()
      } catch {
        // Some browsers resolve onload after decode already; keep the logo usable.
      }

      if (version === loadVersion) {
        loadedSrc.value = src
      }
    }
    image.onerror = () => {
      if (version === loadVersion) {
        loadedSrc.value = src
      }
    }
    image.src = src
  },
  { immediate: true }
)
</script>

<template>
  <span class="app-logo-shell" :class="{ 'app-logo-shell-ready': isReady }">
    <img v-if="loadedSrc" :src="loadedSrc" :alt="alt" class="app-logo-image" decoding="async" />
  </span>
</template>
