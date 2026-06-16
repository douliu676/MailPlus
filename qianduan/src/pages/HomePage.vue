<script setup lang="ts">
import { computed, onMounted, watch } from 'vue'
import { RouterLink } from 'vue-router'
import {
  ArrowRight,
  BarChart3,
  KeyRound,
  LogIn,
  Mail,
  MailCheck,
  Moon,
  Network,
  Sun,
} from 'lucide-vue-next'
import AppLogo from '../components/AppLogo.vue'
import { getAuthToken } from '../api/session'
import { useAppStore } from '../stores/app'
import { useTheme } from '../theme'

const appStore = useAppStore()
const { isDark } = useTheme()
const currentYear = computed(() => new Date().getFullYear())
const siteName = computed(() => appStore.siteName.value)
const siteSubtitle = computed(() => appStore.siteSubtitle.value)
const siteLogoSrc = computed(() => appStore.siteLogo.value)
const isAuthenticated = computed(() => Boolean(getAuthToken()))
const entryPath = computed(() => (isAuthenticated.value ? '/admin/dashboard' : '/login'))
const topEntryText = computed(() => (isAuthenticated.value ? '控制台' : '登录'))
const pageTitle = computed(() => `\u9996\u9875 - ${siteName.value}`)

const featureCards = [
  {
    title: '邮箱账号',
    description: '集中整理 Outlook 与 IMAP。',
    icon: Mail,
    tone: 'teal',
  },
  {
    title: '状态巡检',
    description: '快速查看可用与异常。',
    icon: MailCheck,
    tone: 'green',
  },
  {
    title: '代理环境',
    description: '管理代理配置和任务。',
    icon: Network,
    tone: 'blue',
  },
  {
    title: '卡密分发',
    description: '维护用户、余额和卡密。',
    icon: KeyRound,
    tone: 'amber',
  },
]

onMounted(() => {
  appStore.fetchPublicSettings()
})

watch(pageTitle, (title) => {
  document.title = title
}, { immediate: true })
</script>

<template>
  <div class="home-page">
    <header class="home-header">
      <nav class="home-nav">
        <div class="home-brand">
          <span class="home-logo">
            <AppLogo :src="siteLogoSrc" />
          </span>
          <span class="home-brand-text">
            <span class="home-brand-title">{{ siteName }}</span>
            <span class="home-brand-subtitle">{{ siteSubtitle }}</span>
          </span>
        </div>

        <div class="home-actions-top">
          <button
            class="home-theme-button"
            type="button"
            :aria-label="isDark ? '切换到浅色模式' : '切换到深色模式'"
            @click="isDark = !isDark"
          >
            <Sun v-if="isDark" class="h-4 w-4" />
            <Moon v-else class="h-4 w-4" />
          </button>
          <RouterLink class="home-login-pill" :to="entryPath">
            <LogIn v-if="!isAuthenticated" class="h-3.5 w-3.5" />
            <BarChart3 v-else class="h-3.5 w-3.5" />
            <span>{{ topEntryText }}</span>
          </RouterLink>
        </div>
      </nav>
    </header>

    <main class="home-main">
      <section class="home-intro" aria-label="首页入口">
        <div class="home-container">
          <div class="home-intro-content">
            <h1>让邮箱管理更便捷</h1>
            <RouterLink class="home-cta" :to="entryPath">
              <span>立即开始</span>
              <ArrowRight class="h-5 w-5" />
            </RouterLink>
          </div>
        </div>
      </section>

      <section class="home-features" aria-label="功能介绍">
        <div class="home-container home-feature-grid">
          <article v-for="feature in featureCards" :key="feature.title" class="home-feature-card">
            <span class="home-feature-icon" :class="`home-tone-${feature.tone}`">
              <component :is="feature.icon" class="h-5 w-5" />
            </span>
            <h2>{{ feature.title }}</h2>
            <p>{{ feature.description }}</p>
          </article>
        </div>
      </section>
    </main>

    <footer class="home-footer">
      <div class="home-container">
        <span>&copy; {{ currentYear }} {{ siteName }}. 保留所有权利。</span>
      </div>
    </footer>
  </div>
</template>

<style scoped>
.home-page {
  --home-bg: #f7fafc;
  --home-bg-soft: #eefaf7;
  --home-text: #0f172a;
  --home-muted: #64748b;
  --home-panel: rgb(255 255 255 / 0.72);
  --home-panel-hover: rgb(255 255 255 / 0.92);
  --home-header: transparent;
  --home-shadow: 0 16px 36px rgb(15 23 42 / 0.07);
  display: flex;
  min-height: 100vh;
  flex-direction: column;
  overflow-x: hidden;
  background-color: var(--home-bg);
  background-image:
    linear-gradient(180deg, var(--home-bg-soft), transparent 22rem),
    linear-gradient(90deg, rgb(20 184 166 / 0.03) 1px, transparent 1px),
    linear-gradient(rgb(14 165 233 / 0.03) 1px, transparent 1px);
  background-size: auto, 5rem 5rem, 5rem 5rem;
  color: var(--home-text);
  font-family:
    "Microsoft YaHei",
    "PingFang SC",
    "Noto Sans SC",
    system-ui,
    sans-serif;
}

.dark .home-page {
  --home-bg: #050816;
  --home-bg-soft: #092a31;
  --home-text: #f8fafc;
  --home-muted: #cbd5e1;
  --home-panel: rgb(15 23 42 / 0.58);
  --home-panel-hover: rgb(15 23 42 / 0.78);
  --home-header: transparent;
  --home-shadow: 0 20px 44px rgb(0 0 0 / 0.24);
  background-image:
    linear-gradient(180deg, rgb(9 42 49 / 0.7), transparent 24rem),
    linear-gradient(90deg, rgb(45 212 191 / 0.026) 1px, transparent 1px),
    linear-gradient(rgb(96 165 250 / 0.026) 1px, transparent 1px);
}

.home-container {
  width: min(100% - 2rem, 72rem);
  margin: 0 auto;
}

.home-header {
  position: sticky;
  top: 0;
  z-index: 30;
  background: var(--home-header);
  backdrop-filter: blur(18px);
}

.home-nav {
  display: flex;
  height: 4rem;
  width: min(100% - 2rem, 72rem);
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  margin: 0 auto;
}

.home-brand {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 0.75rem;
}

.home-logo {
  display: flex;
  height: 2.25rem;
  width: 2.25rem;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  border-radius: 0.5rem;
}

.home-brand-text {
  min-width: 0;
}

.home-brand-title,
.home-brand-subtitle {
  display: block;
  max-width: 18rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.home-brand-title {
  font-size: 0.95rem;
  font-weight: 700;
  color: var(--home-text);
}

.home-brand-subtitle {
  margin-top: 0.08rem;
  font-size: 0.75rem;
  font-weight: 500;
  color: var(--home-muted);
}

.home-actions-top {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: 0.55rem;
}

.home-theme-button,
.home-login-pill {
  display: inline-flex;
  height: 2rem;
  align-items: center;
  justify-content: center;
  border-radius: 9999px;
  transition:
    background-color 0.18s ease,
    color 0.18s ease,
    transform 0.18s ease;
}

.home-theme-button {
  width: 2rem;
  color: var(--home-muted);
}

.home-theme-button:hover {
  background: var(--home-panel);
  color: var(--home-text);
}

.home-login-pill {
  gap: 0.35rem;
  background: rgb(15 23 42 / 0.9);
  padding: 0 0.75rem;
  font-size: 0.78rem;
  font-weight: 700;
  color: white;
}

.home-login-pill:hover {
  transform: translateY(-1px);
  background: rgb(15 23 42);
}

.dark .home-login-pill {
  background: rgb(255 255 255 / 0.12);
  color: white;
}

.dark .home-login-pill:hover {
  background: rgb(255 255 255 / 0.18);
}

.home-main {
  display: flex;
  flex: 1;
  flex-direction: column;
  justify-content: center;
  padding: 2.5rem 0 3rem;
}

.home-intro {
  padding: 0 0 2.75rem;
}

.home-intro-content {
  display: flex;
  max-width: 58rem;
  align-items: center;
  justify-content: center;
  gap: 2.8rem;
  margin: 0 auto;
  text-align: center;
  transform: translateY(-1.35rem);
}

.home-intro-content h1 {
  font-size: 3.7rem;
  line-height: 1.02;
  font-weight: 600;
  color: var(--home-text);
}

.home-cta {
  display: inline-flex;
  height: 3.75rem;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  gap: 0.75rem;
  border-radius: 0.5rem;
  background: #0f766e;
  padding: 0 1.95rem;
  font-size: 1.16rem;
  font-weight: 700;
  line-height: 1;
  color: white;
  box-shadow: 0 14px 28px rgb(15 118 110 / 0.18);
  transition:
    background-color 0.18s ease,
    box-shadow 0.18s ease,
    transform 0.18s ease;
  transform: translateY(0.22rem);
}

.home-cta:hover {
  background: #0d9488;
  box-shadow: 0 18px 34px rgb(15 118 110 / 0.24);
  transform: translateY(0.05rem);
}

.home-cta svg {
  transform: translateY(0.04rem);
}

.home-features {
  padding-top: 4.4rem;
}

.home-feature-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 1rem;
}

.home-feature-card {
  min-height: 9rem;
  border-radius: 0.5rem;
  background: var(--home-panel);
  padding: 1rem;
  transition:
    background-color 0.18s ease,
    box-shadow 0.18s ease,
    transform 0.18s ease;
}

.home-feature-card:hover {
  background: var(--home-panel-hover);
  box-shadow: var(--home-shadow);
  transform: translateY(-2px);
}

.home-feature-icon {
  display: inline-flex;
  height: 2.3rem;
  width: 2.3rem;
  align-items: center;
  justify-content: center;
  border-radius: 0.5rem;
}

.home-feature-card h2 {
  margin-top: 0.85rem;
  font-size: 0.98rem;
  font-weight: 700;
  color: var(--home-text);
}

.home-feature-card p {
  margin-top: 0.45rem;
  font-size: 0.82rem;
  line-height: 1.65;
  font-weight: 400;
  color: var(--home-muted);
}

.home-tone-teal {
  background: rgb(20 184 166 / 0.13);
  color: #0d9488;
}

.home-tone-green {
  background: rgb(34 197 94 / 0.13);
  color: #16a34a;
}

.home-tone-blue {
  background: rgb(59 130 246 / 0.14);
  color: #2563eb;
}

.home-tone-amber {
  background: rgb(245 158 11 / 0.16);
  color: #d97706;
}

.dark .home-tone-teal {
  color: #5eead4;
}

.dark .home-tone-green {
  color: #86efac;
}

.dark .home-tone-blue {
  color: #93c5fd;
}

.dark .home-tone-amber {
  color: #fcd34d;
}

.home-footer {
  padding: 1.5rem 0;
  color: var(--home-muted);
}

.home-footer .home-container {
  display: flex;
  justify-content: center;
  text-align: center;
  font-size: 0.78rem;
  font-weight: 500;
}

@media (max-width: 980px) {
  .home-main {
    padding-top: 3.25rem;
  }

  .home-feature-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .home-intro-content {
    flex-direction: column;
    gap: 1.5rem;
  }
}

@media (max-width: 720px) {
  .home-brand-title,
  .home-brand-subtitle {
    max-width: 11rem;
  }

  .home-intro-content h1 {
    font-size: 2.6rem;
  }
}

@media (max-width: 560px) {
  .home-feature-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}

@media (max-width: 460px) {
  .home-container,
  .home-nav {
    width: min(100% - 1.25rem, 72rem);
  }

  .home-brand {
    gap: 0.55rem;
  }

  .home-logo {
    height: 2rem;
    width: 2rem;
  }

  .home-brand-title,
  .home-brand-subtitle {
    max-width: 8.5rem;
  }

  .home-login-pill {
    padding: 0 0.65rem;
  }

  .home-login-pill svg {
    display: none;
  }
}
</style>
