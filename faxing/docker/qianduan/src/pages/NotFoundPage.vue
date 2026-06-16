<script setup lang="ts">
import { computed, onMounted, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { Home, LayoutDashboard, Moon, Sun } from 'lucide-vue-next'
import { getAuthToken } from '../api/session'
import AppLogo from '../components/AppLogo.vue'
import { useAppStore } from '../stores/app'
import { useTheme } from '../theme'

const appStore = useAppStore()
const { isDark } = useTheme()
const siteName = computed(() => appStore.siteName.value)
const siteSubtitle = computed(() => appStore.siteSubtitle.value)
const siteLogoSrc = computed(() => appStore.siteLogo.value)
const pageTitle = computed(() => `页面不存在 - ${siteName.value}`)
const isAuthenticated = computed(() => Boolean(getAuthToken()))
const returnPath = computed(() => (isAuthenticated.value ? '/admin/dashboard' : '/home'))
const returnText = computed(() => (isAuthenticated.value ? '返回后台' : '返回首页'))

onMounted(() => {
  void appStore.fetchPublicSettings()
})

watch(pageTitle, (title) => {
  document.title = title
}, { immediate: true })
</script>

<template>
  <div class="not-found-page">
    <header class="not-found-header">
      <div class="not-found-brand">
        <span class="not-found-logo">
          <AppLogo :src="siteLogoSrc" :alt="siteName" />
        </span>
        <span class="not-found-brand-copy">
          <strong>{{ siteName }}</strong>
          <span>{{ siteSubtitle }}</span>
        </span>
      </div>

      <button
        class="not-found-theme-button"
        type="button"
        :aria-label="isDark ? '切换到浅色模式' : '切换到深色模式'"
        @click="isDark = !isDark"
      >
        <Sun v-if="isDark" class="h-5 w-5" />
        <Moon v-else class="h-5 w-5" />
      </button>
    </header>

    <main class="not-found-main">
      <section class="not-found-panel" aria-label="页面不存在">
        <p class="not-found-code">404</p>
        <h1>页面不存在</h1>
        <RouterLink class="not-found-link" :to="returnPath">
          <LayoutDashboard v-if="isAuthenticated" class="h-4 w-4" />
          <Home v-else class="h-4 w-4" />
          <span>{{ returnText }}</span>
        </RouterLink>
      </section>
    </main>
  </div>
</template>

<style scoped>
.not-found-page {
  --not-found-bg: #f8fafc;
  --not-found-bg-soft: #ecfdf5;
  --not-found-text: #0f172a;
  --not-found-muted: #64748b;
  --not-found-panel: rgb(255 255 255 / 0.82);
  --not-found-border: rgb(226 232 240 / 0.9);
  --not-found-shadow: 0 24px 70px rgb(15 23 42 / 0.1);
  min-height: 100vh;
  overflow-x: hidden;
  background-color: var(--not-found-bg);
  background-image:
    linear-gradient(180deg, var(--not-found-bg-soft), transparent 24rem),
    linear-gradient(90deg, rgb(20 184 166 / 0.04) 1px, transparent 1px),
    linear-gradient(rgb(14 165 233 / 0.04) 1px, transparent 1px);
  background-size: auto, 4.5rem 4.5rem, 4.5rem 4.5rem;
  color: var(--not-found-text);
  font-family:
    "Microsoft YaHei",
    "PingFang SC",
    "Noto Sans SC",
    system-ui,
    sans-serif;
}

.dark .not-found-page {
  --not-found-bg: #050816;
  --not-found-bg-soft: #092a31;
  --not-found-text: #f8fafc;
  --not-found-muted: #cbd5e1;
  --not-found-panel: rgb(15 23 42 / 0.74);
  --not-found-border: rgb(51 65 85 / 0.82);
  --not-found-shadow: 0 26px 76px rgb(0 0 0 / 0.32);
  background-image:
    linear-gradient(180deg, rgb(9 42 49 / 0.74), transparent 25rem),
    linear-gradient(90deg, rgb(45 212 191 / 0.03) 1px, transparent 1px),
    linear-gradient(rgb(96 165 250 / 0.03) 1px, transparent 1px);
}

.not-found-header {
  position: relative;
  z-index: 2;
  display: flex;
  width: min(100% - 2rem, 72rem);
  height: 4.25rem;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  margin: 0 auto;
}

.not-found-brand {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 0.75rem;
}

.not-found-logo {
  display: inline-flex;
  width: 2.35rem;
  height: 2.35rem;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  border-radius: 0.6rem;
}

.not-found-brand-copy {
  display: grid;
  min-width: 0;
  line-height: 1.2;
}

.not-found-brand-copy strong,
.not-found-brand-copy span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.not-found-brand-copy strong {
  font-size: 0.95rem;
  font-weight: 800;
  color: var(--not-found-text);
}

.not-found-brand-copy span {
  margin-top: 0.2rem;
  font-size: 0.75rem;
  color: var(--not-found-muted);
}

.not-found-theme-button {
  display: inline-flex;
  width: 2.5rem;
  height: 2.5rem;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--not-found-border);
  border-radius: 0.75rem;
  background: var(--not-found-panel);
  color: var(--not-found-muted);
  box-shadow: 0 10px 28px rgb(15 23 42 / 0.08);
  transition:
    border-color 0.18s ease,
    color 0.18s ease,
    transform 0.18s ease;
}

.not-found-theme-button:hover {
  color: #0f766e;
  transform: translateY(-1px);
}

.dark .not-found-theme-button:hover {
  color: #5eead4;
}

.not-found-main {
  display: grid;
  min-height: calc(100vh - 4.25rem);
  place-items: center;
  padding: 2rem 1rem 4rem;
}

.not-found-panel {
  width: min(100%, 32rem);
  border: 1px solid var(--not-found-border);
  border-radius: 1rem;
  background: var(--not-found-panel);
  box-shadow: var(--not-found-shadow);
  padding: clamp(2rem, 6vw, 3rem);
  text-align: center;
  backdrop-filter: blur(18px);
}

.not-found-code {
  margin: 0 0 0.75rem;
  background: linear-gradient(135deg, #ef4444, #dc2626);
  background-clip: text;
  -webkit-background-clip: text;
  color: transparent;
  font-size: clamp(3.5rem, 14vw, 6rem);
  font-weight: 900;
  line-height: 0.95;
}

.not-found-panel h1 {
  margin: 0;
  font-size: clamp(1.4rem, 4vw, 2rem);
  font-weight: 900;
  letter-spacing: 0;
  color: var(--not-found-text);
}

.not-found-link {
  display: inline-flex;
  min-height: 2.75rem;
  align-items: center;
  justify-content: center;
  gap: 0.5rem;
  margin-top: 1.45rem;
  border-radius: 0.75rem;
  background: linear-gradient(135deg, #14b8a6, #2563eb);
  padding: 0 1.15rem;
  color: #fff;
  font-size: 0.9rem;
  font-weight: 800;
  text-decoration: none;
  box-shadow: 0 18px 34px rgb(20 184 166 / 0.24);
  transition:
    box-shadow 0.18s ease,
    transform 0.18s ease;
}

.not-found-link:hover {
  box-shadow: 0 20px 40px rgb(37 99 235 / 0.24);
  transform: translateY(-1px);
}

.not-found-link:focus-visible,
.not-found-theme-button:focus-visible {
  outline: none;
  box-shadow: 0 0 0 3px rgb(20 184 166 / 0.28);
}

@media (max-width: 520px) {
  .not-found-header {
    width: min(100% - 1rem, 72rem);
  }

  .not-found-logo {
    width: 2rem;
    height: 2rem;
  }

  .not-found-brand-copy span {
    display: none;
  }

  .not-found-panel {
    border-radius: 0.85rem;
    padding: 1.75rem 1.2rem;
  }
}
</style>
