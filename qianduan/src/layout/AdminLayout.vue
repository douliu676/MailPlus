<script setup lang="ts">
import { computed, defineComponent, h, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import {
  AlertCircle,
  BarChart3,
  ChevronDown,
  ChevronsLeft,
  ChevronsRight,
  CheckCircle2,
  CircleUser,
  ExternalLink,
  Github,
  KeyRound,
  LogOut,
  Menu,
  Moon,
  RefreshCw,
  ScrollText,
  Settings,
  Sun,
  User,
  Users,
  X,
} from 'lucide-vue-next'
import AppLogo from '../components/AppLogo.vue'
import TaskCenter from '../components/TaskCenter.vue'
import { useAppStore } from '../stores/app'
import { useTheme } from '../theme'
import { clearAuthSession, getSessionItem, setSessionItem } from '../api/session'
import { checkAppUpdate, type UpdateCheckResult } from '../api/adminSettings'

const sidebarStorageKey = 'admin_sidebar_collapsed'
const route = useRoute()
const router = useRouter()
const appStore = useAppStore()
const sidebarCollapsed = ref(localStorage.getItem(sidebarStorageKey) !== 'false')
const mobileSidebarOpen = ref(false)
const userDropdownOpen = ref(false)
const userDropdownRef = ref<HTMLElement | null>(null)
const versionPanelOpen = ref(false)
const versionPanelRef = ref<HTMLElement | null>(null)
const updateChecking = ref(false)
const updateCheckResult = ref<UpdateCheckResult | null>(null)
const { isDark } = useTheme()
const siteVersion = ref('v1.0.1')

const ImapMailIcon = defineComponent({
  name: 'ImapMailIcon',
  inheritAttrs: false,
  setup(_, { attrs }) {
    return () =>
      h(
        'svg',
        {
          ...attrs,
          xmlns: 'http://www.w3.org/2000/svg',
          viewBox: '0 0 24 24',
          fill: 'none',
          stroke: 'currentColor',
          'stroke-width': '1.8',
          'stroke-linecap': 'round',
          'stroke-linejoin': 'round',
          'aria-hidden': 'true',
        },
        [
          h('path', { d: 'M4.2 6.8h15.6A1.8 1.8 0 0 1 21.6 8.6v8.8a1.8 1.8 0 0 1-1.8 1.8H4.2a1.8 1.8 0 0 1-1.8-1.8V8.6a1.8 1.8 0 0 1 1.8-1.8Z', 'stroke-width': '2.15' }),
          h('path', { d: 'm3.4 8 8.6 6.2L20.6 8', 'stroke-width': '2.15' }),
          h('path', { d: 'm4.6 17.2 5.25-4.25', 'stroke-width': '1.75' }),
          h('path', { d: 'm19.4 17.2-5.25-4.25', 'stroke-width': '1.75' }),
        ]
      )
  },
})

const MicrosoftMailIcon = defineComponent({
  name: 'MicrosoftMailIcon',
  inheritAttrs: false,
  setup(_, { attrs }) {
    return () =>
      h(
        'svg',
        {
          ...attrs,
          xmlns: 'http://www.w3.org/2000/svg',
          viewBox: '0 0 24 24',
          fill: 'none',
          stroke: 'currentColor',
          'stroke-width': '1.8',
          'stroke-linecap': 'round',
          'stroke-linejoin': 'round',
          'aria-hidden': 'true',
        },
        [
          h('rect', { x: '3.2', y: '3.2', width: '7.8', height: '7.8', rx: '0.8', stroke: 'none', fill: '#f25022' }),
          h('rect', { x: '13', y: '3.2', width: '7.8', height: '7.8', rx: '0.8', stroke: 'none', fill: '#7fba00' }),
          h('rect', { x: '3.2', y: '13', width: '7.8', height: '7.8', rx: '0.8', stroke: 'none', fill: '#00a4ef' }),
          h('rect', { x: '13', y: '13', width: '7.8', height: '7.8', rx: '0.8', stroke: 'none', fill: '#ffb900' }),
        ]
      )
  },
})

const ProxySystemIcon = defineComponent({
  name: 'ProxySystemIcon',
  inheritAttrs: false,
  setup(_, { attrs }) {
    return () =>
      h(
        'svg',
        {
          ...attrs,
          xmlns: 'http://www.w3.org/2000/svg',
          viewBox: '0 0 24 24',
          fill: 'none',
          stroke: 'currentColor',
          'stroke-width': '1.9',
          'stroke-linecap': 'round',
          'stroke-linejoin': 'round',
          'aria-hidden': 'true',
        },
        [
          h('circle', { cx: '12', cy: '12', r: '8.9', fill: 'currentColor', 'fill-opacity': '0.12', 'stroke-width': '2.1' }),
          h('path', { d: 'M3.1 12h17.8', 'stroke-width': '2.05' }),
          h('path', { d: 'M12 3.1c2.15 2.35 3.3 5.35 3.3 8.9s-1.15 6.55-3.3 8.9', 'stroke-width': '2.05' }),
          h('path', { d: 'M12 3.1C9.85 5.45 8.7 8.45 8.7 12s1.15 6.55 3.3 8.9', 'stroke-width': '2.05' }),
          h('path', { d: 'M4.65 7.45h14.7', opacity: '0.48' }),
          h('path', { d: 'M4.65 16.55h14.7', opacity: '0.48' }),
        ]
      )
  },
})

const navIconPalette = ['#38bdf8', '#a78bfa', '#2dd4bf', '#f97316', '#facc15', '#fb7185', '#34d399']

const rawNavGroups = [
  [
    { path: '/admin/dashboard', label: '\u4eea\u8868\u76d8', icon: BarChart3, iconColor: '#38bdf8' },
  ],
  [
    { path: '/admin/outlook-accounts', label: '微软邮箱管理', icon: MicrosoftMailIcon },
    { path: '/admin/accounts', label: 'IMAP邮箱管理', icon: ImapMailIcon, iconColor: '#60a5fa' },
  ],
  [
    { path: '/admin/proxy-system', label: '代理系统', icon: ProxySystemIcon, iconColor: '#0ea5e9' },
    { path: '/admin/card-keys', label: '卡密系统', icon: KeyRound, iconColor: '#f59e0b' },
    { path: '/admin/card-key-logs', label: '卡密日志', icon: ScrollText, iconColor: '#14b8a6' },
  ],
  [
    { path: '/admin/users', label: '\u7528\u6237\u7ba1\u7406', icon: Users, iconColor: '#a78bfa' },
    { path: '/admin/profile', label: '\u4e2a\u4eba\u8d44\u6599', icon: User, iconColor: '#34d399' },
    { path: '/admin/settings', label: '\u7cfb\u7edf\u8bbe\u7f6e', icon: Settings, iconColor: '#f97316' },
  ],
]

let navIconIndex = 0
const navGroups = rawNavGroups.map((group) =>
  group.map((item) => ({
    ...item,
    iconColor: item.iconColor || navIconPalette[navIconIndex++ % navIconPalette.length],
  }))
)

function readStoredUser() {
  const raw = getSessionItem('auth_user')
  if (!raw) {
    return { id: 1, username: 'admin', email: '', role: 'admin', avatar_url: '', balance: 0 }
  }

  try {
    return { id: 1, username: 'admin', email: '', role: 'admin', avatar_url: '', balance: 0, ...JSON.parse(raw) }
  } catch {
    return { id: 1, username: 'admin', email: '', role: 'admin', avatar_url: '', balance: 0 }
  }
}

const userSnapshot = ref(readStoredUser())
const user = computed(() => userSnapshot.value)

const pageTitle = computed(() => String(route.meta.title || '\u4eea\u8868\u76d8'))
const siteLogoSrc = computed(() => appStore.siteLogo.value)
const displayName = computed(() => user.value.username || user.value.email?.split('@')[0] || 'admin')
const userInitials = computed(() => displayName.value.slice(0, 2).toUpperCase())
const userAvatar = computed(() => user.value.avatar_url || '')
const sidebarExpanded = computed(() => mobileSidebarOpen.value || !sidebarCollapsed.value)
const userBalance = computed(() => {
  const value = Number(user.value.balance || 0)
  return Number.isFinite(value) ? value : 0
})
const formattedBalance = computed(() => {
  return `$${userBalance.value.toFixed(2)}`
})
const versionStatusText = computed(() => {
  if (updateChecking.value) return '检查中'
  switch (updateCheckResult.value?.status) {
    case 'outdated':
      return '有更新'
    case 'latest':
      return '已最新'
    case 'error':
      return '检查失败'
    default:
      return '版本'
  }
})
const versionPanelTitle = computed(() => {
  switch (updateCheckResult.value?.status) {
    case 'outdated':
      return '发现新版本'
    case 'latest':
      return '已是最新版本'
    case 'error':
      return '检查失败'
    default:
      return '当前版本'
  }
})
const versionStatusIcon = computed(() => {
  if (updateCheckResult.value?.status === 'latest') return CheckCircle2
  if (updateCheckResult.value?.status === 'outdated' || updateCheckResult.value?.status === 'error') return AlertCircle
  return RefreshCw
})
const versionDotClass = computed(() => ({
  'admin-version-dot-update': updateCheckResult.value?.status === 'outdated',
  'admin-version-dot-error': updateCheckResult.value?.status === 'error',
}))
const shouldShowVersionDot = computed(() => updateCheckResult.value?.status === 'outdated' || updateCheckResult.value?.status === 'error')
const versionThemeClass = computed(() => (isDark.value ? 'admin-version-dark' : 'admin-version-light'))
const updateReleaseURL = computed(() => updateCheckResult.value?.release_url || updateCheckResult.value?.source_url || '')
let profileRefreshTimer: number | undefined
let lastViewportBucket = ''

onMounted(() => {
  appStore.fetchPublicSettings()
  refreshProfile()
  syncResponsiveSidebar()
  profileRefreshTimer = window.setInterval(refreshProfile, 15000)
  document.addEventListener('click', handleClickOutside)
  document.addEventListener('keydown', handleKeydown)
  window.addEventListener('storage', refreshStoredUser)
  window.addEventListener('resize', syncResponsiveSidebar)
  window.setTimeout(() => refreshUpdateStatus(false), 600)
})

onBeforeUnmount(() => {
  window.clearInterval(profileRefreshTimer)
  document.body.style.overflow = ''
  document.removeEventListener('click', handleClickOutside)
  document.removeEventListener('keydown', handleKeydown)
  window.removeEventListener('storage', refreshStoredUser)
  window.removeEventListener('resize', syncResponsiveSidebar)
})

watch(
  () => [route.meta.title, appStore.siteName.value],
  () => {
    document.title = `${pageTitle.value} - ${appStore.siteName.value}`
  },
  { immediate: true }
)

watch(
  () => route.fullPath,
  () => {
    closeMobileSidebar()
  }
)

watch(mobileSidebarOpen, (open) => {
  document.body.style.overflow = open && window.innerWidth < 768 ? 'hidden' : ''
})

function logout() {
  closeUserDropdown()
  clearAuthSession()
  router.replace('/login')
}

function toggleVersionPanel() {
  versionPanelOpen.value = !versionPanelOpen.value
  if (versionPanelOpen.value && !updateCheckResult.value) {
    refreshUpdateStatus(false)
  }
}

function closeVersionPanel() {
  versionPanelOpen.value = false
}

async function refreshUpdateStatus(showToast = true) {
  if (updateChecking.value) return
  updateChecking.value = true
  try {
    const result = await checkAppUpdate(showToast)
    updateCheckResult.value = result
    if (result.current_version) {
      siteVersion.value = result.current_version
    }
    if (showToast) {
      if (result.status === 'outdated') {
        appStore.showWarning(`发现新版本 ${result.latest_version || ''}`.trim())
      } else if (result.status === 'latest') {
        appStore.showSuccess('已是最新版本')
      } else {
        appStore.showError(result.message || '检查更新失败')
      }
    }
  } catch (error) {
    updateCheckResult.value = {
      current_version: siteVersion.value,
      latest_version: '',
      has_update: false,
      status: 'error',
      source_url: '',
      release_url: '',
      message: error instanceof Error ? error.message : '检查更新失败',
      checked_at: new Date().toISOString(),
    }
    if (showToast) {
      appStore.showError(updateCheckResult.value.message)
    }
  } finally {
    updateChecking.value = false
  }
}

function openUpdateRelease() {
  if (!updateReleaseURL.value) return
  window.open(updateReleaseURL.value, '_blank', 'noopener,noreferrer')
}

function toggleSidebar() {
  sidebarCollapsed.value = !sidebarCollapsed.value
  localStorage.setItem(sidebarStorageKey, String(sidebarCollapsed.value))
}

function toggleHeaderSidebar() {
  if (window.innerWidth < 768) {
    openMobileSidebar()
    return
  }

  closeMobileSidebar()
  toggleSidebar()
}

function openMobileSidebar() {
  mobileSidebarOpen.value = true
}

function closeMobileSidebar() {
  mobileSidebarOpen.value = false
}

function syncResponsiveSidebar() {
  const width = window.innerWidth
  const viewportBucket = width < 768 ? 'mobile' : width < 1280 ? 'compact' : 'desktop'
  if (width >= 768) {
    closeMobileSidebar()
  }
  if (viewportBucket === 'compact' && lastViewportBucket !== 'compact') {
    sidebarCollapsed.value = true
  }
  lastViewportBucket = viewportBucket
}

function toggleUserDropdown() {
  userDropdownOpen.value = !userDropdownOpen.value
}

function closeUserDropdown() {
  userDropdownOpen.value = false
}

function refreshStoredUser() {
  userSnapshot.value = readStoredUser()
}

async function refreshProfile() {
  const id = Number(userSnapshot.value.id || 1)
  try {
    const response = await fetch('/api/user/profile', {
      headers: {
        'Content-Type': 'application/json',
        'X-User-ID': String(id),
      },
    })
    const result = await response.json().catch(() => null)
    if (!response.ok || result?.code !== 0 || !result.data) return

    userSnapshot.value = { ...userSnapshot.value, ...result.data }
    setSessionItem('auth_user', JSON.stringify(userSnapshot.value))
  } catch {
    // Ignore profile refresh failures so the current page stays usable.
  }
}

function handleClickOutside(event: MouseEvent) {
  if (userDropdownRef.value && !userDropdownRef.value.contains(event.target as Node)) {
    closeUserDropdown()
  }
  if (versionPanelRef.value && !versionPanelRef.value.contains(event.target as Node)) {
    closeVersionPanel()
  }
}

function handleKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    closeMobileSidebar()
    closeUserDropdown()
    closeVersionPanel()
  }
}
</script>

<template>
  <div class="min-h-screen bg-gray-50 text-gray-900 dark:bg-dark-950 dark:text-white">
    <div v-if="mobileSidebarOpen" class="admin-sidebar-backdrop fixed inset-0 z-20 bg-black/40 backdrop-blur-sm md:hidden" @click="closeMobileSidebar"></div>

    <aside
      class="admin-sidebar fixed inset-y-0 left-0 z-30 flex flex-col overflow-visible border-r border-gray-200 bg-white/95 shadow-sm backdrop-blur dark:border-dark-800 dark:bg-dark-900/95"
      :class="[sidebarExpanded ? 'w-[232px]' : 'w-[68px]', { 'admin-sidebar-mobile-open': mobileSidebarOpen }]"
    >
      <div class="admin-sidebar-brand flex h-16 items-center gap-3 border-b border-gray-200 px-4 dark:border-dark-800" :class="{ 'justify-center px-0': !sidebarExpanded }">
        <div class="sidebar-logo flex shrink-0 items-center justify-center overflow-hidden rounded-xl" :class="sidebarExpanded ? 'h-9 w-9' : 'h-8 w-8'">
          <AppLogo :src="siteLogoSrc" />
        </div>
        <div v-if="sidebarExpanded" ref="versionPanelRef" class="sidebar-label-fade relative min-w-0 flex-1">
          <span class="block truncate text-lg font-bold leading-tight text-gray-900 dark:text-white">{{ appStore.siteName.value }}</span>
          <button
            class="admin-version-button mt-1 inline-flex items-center gap-1.5 px-2 py-0.5 text-xs font-medium transition-colors"
            :class="versionThemeClass"
            type="button"
            :title="versionStatusText"
            @click.stop="toggleVersionPanel"
          >
            {{ siteVersion }}
            <span v-if="shouldShowVersionDot" class="admin-version-dot" :class="versionDotClass" aria-hidden="true"></span>
          </button>

          <transition name="dropdown">
            <div
              v-if="versionPanelOpen"
              class="admin-version-panel absolute left-0 top-full z-40 mt-3 w-64 overflow-hidden p-4"
              :class="versionThemeClass"
            >
              <div class="flex items-center justify-between gap-3">
                <div class="admin-version-heading">当前版本</div>
                <button
                  class="admin-version-refresh"
                  type="button"
                  title="刷新"
                  :disabled="updateChecking"
                  @click.stop="refreshUpdateStatus(true)"
                >
                  <RefreshCw class="h-4 w-4" :class="{ 'animate-spin': updateChecking }" />
                </button>
              </div>

              <div class="mt-5 flex flex-col items-center text-center">
                <div class="admin-version-number-row">
                  <span class="admin-version-number">{{ siteVersion }}</span>
                  <span class="admin-version-status-badge" :class="`admin-version-status-${updateCheckResult?.status || 'idle'}`" aria-hidden="true">
                    <component :is="versionStatusIcon" class="h-3.5 w-3.5" :class="{ 'animate-spin': updateChecking }" />
                  </span>
                </div>
                <div class="admin-version-subtitle" :class="`admin-version-subtitle-${updateCheckResult?.status || 'idle'}`">
                  <span>{{ updateChecking ? '正在检查更新' : versionPanelTitle }}</span>
                </div>
                <div v-if="updateCheckResult?.latest_version && updateCheckResult.latest_version !== siteVersion" class="admin-version-latest">
                  最新版本 {{ updateCheckResult.latest_version }}
                </div>
                <div v-if="updateCheckResult?.message && updateCheckResult.status === 'error'" class="admin-version-error">
                  {{ updateCheckResult.message }}
                </div>
              </div>

              <button
                class="admin-version-release mt-5 inline-flex w-full items-center justify-center gap-2 rounded-lg px-3 py-2 text-sm font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50"
                type="button"
                :disabled="!updateReleaseURL"
                @click.stop="openUpdateRelease"
              >
                <Github v-if="updateReleaseURL" class="h-4 w-4" />
                <ExternalLink v-else class="h-4 w-4" />
                <span>查看发布</span>
              </button>
            </div>
          </transition>
        </div>
        <button
          v-if="mobileSidebarOpen"
          class="admin-mobile-close-button md:hidden"
          type="button"
          aria-label="Close menu"
          @click="closeMobileSidebar"
        >
          <X class="h-5 w-5" />
        </button>
      </div>

      <nav class="flex-1 overflow-y-auto py-4" :class="sidebarExpanded ? 'px-3' : 'px-2'">
        <div v-for="(group, groupIndex) in navGroups" :key="groupIndex" class="admin-nav-group">
          <RouterLink
            v-for="item in group"
            :key="item.path"
            :to="item.path"
            class="admin-nav-link group h-11 items-center rounded-xl px-3 text-sm font-medium text-gray-600 transition-colors hover:bg-primary-50 hover:text-primary-700 dark:text-dark-300 dark:hover:bg-dark-800 dark:hover:text-primary-300"
            :class="[
              { 'bg-primary-50 text-primary-700 dark:bg-dark-800 dark:text-primary-300': route.path === item.path },
              !sidebarExpanded ? 'admin-nav-link-collapsed px-0' : '',
            ]"
            @click="closeMobileSidebar"
          >
            <component :is="item.icon" class="admin-nav-icon h-5 w-5 shrink-0" :style="{ color: item.iconColor }" />
            <span v-if="sidebarExpanded" class="sidebar-label-fade sidebar-nav-label">{{ item.label }}</span>
          </RouterLink>
        </div>
      </nav>

      <div class="admin-sidebar-bottom border-t border-gray-200 p-3 dark:border-dark-800">
        <div class="admin-sidebar-tool-group" :class="{ 'admin-sidebar-tool-group-collapsed': !sidebarExpanded }">
          <TaskCenter variant="sidebar" :collapsed="!sidebarExpanded" />
          <button
            class="admin-sidebar-tool-button"
            :class="{ 'admin-sidebar-tool-button-collapsed': !sidebarExpanded }"
            type="button"
            :title="isDark ? '切换亮色模式' : '切换暗色模式'"
            @click="isDark = !isDark"
          >
            <Sun v-if="isDark" class="admin-sidebar-theme-icon admin-sidebar-theme-icon-sun h-5 w-5" />
            <Moon v-else class="admin-sidebar-theme-icon admin-sidebar-theme-icon-moon h-5 w-5" />
            <span v-if="sidebarExpanded" class="sidebar-label-fade sidebar-nav-label text-sm font-medium">
              {{ isDark ? '亮色模式' : '暗色模式' }}
            </span>
          </button>
        </div>

        <div class="admin-sidebar-collapse">
          <button
            class="flex h-10 w-full items-center rounded-xl px-3 text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-primary-300"
            :class="sidebarCollapsed ? 'justify-center px-0' : 'justify-start gap-3'"
            type="button"
            :title="sidebarCollapsed ? '\u5c55\u5f00' : '\u6536\u8d77'"
            @click="toggleSidebar"
          >
            <ChevronsRight v-if="sidebarCollapsed" class="h-5 w-5" />
            <ChevronsLeft v-else class="h-5 w-5" />
            <span v-if="!sidebarCollapsed" class="sidebar-label-fade sidebar-nav-label text-sm font-medium">&#25910;&#36215;</span>
          </button>
        </div>
      </div>
    </aside>

    <div class="admin-content-shell relative min-h-screen overflow-hidden" :class="sidebarCollapsed ? 'pl-[68px]' : 'pl-[232px]'">
      <header class="sticky top-0 z-20 flex h-16 items-center justify-between border-b border-gray-200 bg-white/80 px-6 backdrop-blur dark:border-dark-800 dark:bg-dark-900/80">
        <div class="flex min-w-0 items-center gap-3">
          <button class="admin-mobile-menu-button" type="button" aria-label="切换侧边栏" title="切换侧边栏" @click="toggleHeaderSidebar">
            <Menu class="h-5 w-5" />
          </button>
          <div class="min-w-0">
            <h1 class="truncate text-xl font-bold">{{ pageTitle }}</h1>
          </div>
        </div>

        <div class="flex min-w-0 shrink-0 items-center gap-3">
          <div
            class="hidden h-8 items-center gap-2 rounded-xl bg-primary-50 px-3 text-sm font-semibold text-primary-700 dark:bg-primary-900/20 dark:text-primary-300 sm:flex"
            title="璐︽埛浣欓"
          >
            <svg
              class="h-4 w-4 text-primary-600 dark:text-primary-400"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              stroke-width="1.5"
              aria-hidden="true"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                d="M2.25 18.75a60.07 60.07 0 0115.797 2.101c.727.198 1.453-.342 1.453-1.096V18.75M3.75 4.5v.75A.75.75 0 013 6h-.75m0 0v-.375c0-.621.504-1.125 1.125-1.125H20.25M2.25 6v9m18-10.5v.75c0 .414.336.75.75.75h.75m-1.5-1.5h.375c.621 0 1.125.504 1.125 1.125v9.75c0 .621-.504 1.125-1.125 1.125h-.375m1.5-1.5H21a.75.75 0 00-.75.75v.75m0 0H3.75m0 0h-.375a1.125 1.125 0 01-1.125-1.125V15m1.5 1.5v-.75A.75.75 0 003 15h-.75M15 10.5a3 3 0 11-6 0 3 3 0 016 0zm3 0h.008v.008H18V10.5zm-12 0h.008v.008H6V10.5z"
              />
            </svg>
            <span>{{ formattedBalance }}</span>
          </div>

          <div ref="userDropdownRef" class="relative">
            <button
              class="flex items-center gap-2 rounded-xl p-1.5 transition-colors hover:bg-gray-100 dark:hover:bg-dark-800"
              type="button"
              aria-label="User Menu"
              @click="toggleUserDropdown"
            >
              <div class="flex h-8 w-8 items-center justify-center overflow-hidden rounded-xl bg-gradient-to-br from-primary-500 to-primary-600 text-sm font-medium text-white shadow-sm">
                <img v-if="userAvatar" :src="userAvatar" :alt="displayName" class="h-full w-full object-cover" />
                <span v-else>{{ userInitials }}</span>
              </div>
              <div class="hidden text-left md:block">
                <div class="text-sm font-medium text-gray-900 dark:text-white">{{ displayName }}</div>
                <div class="text-xs capitalize text-gray-500 dark:text-dark-400">{{ user.role }}</div>
              </div>
              <ChevronDown class="hidden h-4 w-4 text-gray-400 md:block" />
            </button>

            <transition name="dropdown">
              <div
                v-if="userDropdownOpen"
                class="dropdown-menu absolute right-0 mt-2 w-56 overflow-hidden rounded-xl border border-gray-200 bg-white py-1 shadow-xl shadow-gray-900/10 dark:border-dark-700 dark:bg-dark-800 dark:shadow-black/30"
              >
                <div class="border-b border-gray-100 px-4 py-3 dark:border-dark-700">
                  <div class="text-sm font-medium text-gray-900 dark:text-white">{{ displayName }}</div>
                  <div class="text-xs text-gray-500 dark:text-dark-400">{{ user.email }}</div>
                </div>

                <div class="py-1">
                  <RouterLink class="dropdown-item" to="/admin/profile" @click="closeUserDropdown">
                    <CircleUser class="h-4 w-4" />
                    <span>&#20010;&#20154;&#36164;&#26009;</span>
                  </RouterLink>
                </div>

                <div class="border-t border-gray-100 py-1 dark:border-dark-700">
                  <button class="dropdown-item w-full text-red-600 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-900/20" type="button" @click="logout">
                    <LogOut class="h-4 w-4" />
                    <span>&#36864;&#20986;&#30331;&#24405;</span>
                  </button>
                </div>
              </div>
            </transition>
          </div>
        </div>
      </header>

      <main class="admin-main relative z-10 p-4 md:p-6 lg:p-8">
        <div class="admin-main-content">
          <RouterView />
        </div>
      </main>
    </div>
  </div>
</template>

<style scoped>
.admin-sidebar {
  transition:
    width 220ms cubic-bezier(0.22, 1, 0.36, 1),
    transform 220ms cubic-bezier(0.22, 1, 0.36, 1);
  will-change: width, transform;
}

.admin-content-shell {
  transition: padding-left 220ms cubic-bezier(0.22, 1, 0.36, 1);
  will-change: padding-left;
}

.admin-main {
  min-width: 0;
  overflow-x: clip;
}

.admin-main-content {
  width: 100%;
  max-width: 2240px;
  min-width: 0;
  margin: 0 auto;
}

.admin-mobile-menu-button,
.admin-mobile-close-button {
  display: inline-flex;
  height: 2.25rem;
  width: 2.25rem;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  border-radius: 0.75rem;
  color: rgb(75 85 99);
  transition:
    background-color 160ms ease,
    color 160ms ease,
    transform 160ms ease;
}

.admin-mobile-menu-button:hover,
.admin-mobile-close-button:hover {
  background: rgb(243 244 246);
  color: rgb(13 148 136);
}

.admin-mobile-menu-button:active,
.admin-mobile-close-button:active {
  transform: scale(0.96);
}

.dark .admin-mobile-menu-button,
.dark .admin-mobile-close-button {
  color: rgb(203 213 225);
}

.dark .admin-mobile-menu-button:hover,
.dark .admin-mobile-close-button:hover {
  background: rgb(30 41 59);
  color: rgb(94 234 212);
}

.admin-sidebar-backdrop {
  animation: admin-sidebar-backdrop-in 180ms ease both;
}

@keyframes admin-sidebar-backdrop-in {
  from {
    opacity: 0;
  }

  to {
    opacity: 1;
  }
}

.admin-nav-group {
  display: grid;
  gap: 0.25rem;
}

.admin-nav-group + .admin-nav-group {
  margin-top: 0.85rem;
  padding-top: 0.85rem;
  border-top: 1px solid rgb(226 232 240 / 0.8);
}

.dark .admin-nav-group + .admin-nav-group {
  border-top-color: rgb(51 65 85 / 0.72);
}

.admin-sidebar-bottom {
  flex-shrink: 0;
}

.admin-sidebar-tool-group {
  display: grid;
  gap: 0.35rem;
  margin-bottom: 1.45rem;
}

.admin-sidebar-tool-group-collapsed {
  justify-items: center;
}

.admin-sidebar-tool-button {
  display: flex;
  width: 100%;
  height: 2.5rem;
  align-items: center;
  gap: 0.75rem;
  border-radius: 0.75rem;
  padding: 0 0.75rem;
  color: rgb(75 85 99);
  transition: background-color 160ms ease, color 160ms ease;
}

.admin-sidebar-tool-button:hover {
  background: rgb(243 244 246);
  color: rgb(13 148 136);
}

.admin-sidebar-theme-icon {
  flex-shrink: 0;
}

.admin-sidebar-theme-icon-sun {
  color: rgb(245 158 11);
}

.admin-sidebar-theme-icon-moon {
  color: rgb(96 165 250);
}

.admin-sidebar-tool-button-collapsed {
  width: 2.5rem;
  justify-content: center;
  padding: 0;
}

.dark .admin-sidebar-tool-button {
  color: rgb(203 213 225);
}

.dark .admin-sidebar-tool-button:hover {
  background: rgb(30 41 59);
  color: rgb(94 234 212);
}

.admin-sidebar-collapse {
  border-top: 1px solid rgb(226 232 240 / 0.8);
  padding-top: 0.85rem;
}

.dark .admin-sidebar-collapse {
  border-top-color: rgb(51 65 85 / 0.72);
}

.admin-nav-link {
  display: grid;
  grid-template-columns: 1.25rem minmax(0, 1fr);
  column-gap: 0.75rem;
}

.admin-nav-link-collapsed {
  grid-template-columns: 1.25rem;
  justify-content: center;
}

.admin-nav-icon {
  justify-self: center;
}

.admin-sidebar-brand,
nav,
nav a,
.admin-sidebar button {
  transition:
    padding 220ms cubic-bezier(0.22, 1, 0.36, 1),
    background-color 160ms ease,
    color 160ms ease;
}

.sidebar-nav-label {
  min-width: 0;
  overflow: hidden;
  line-height: 1;
  white-space: nowrap;
}

.sidebar-label-fade {
  animation: sidebar-label-fade-in 120ms ease both;
}

.admin-version-button {
  max-width: 100%;
  min-height: 1.25rem;
  border-radius: 0.5rem;
  background: rgb(241 245 249);
  color: rgb(71 85 105);
  letter-spacing: 0;
  box-shadow: inset 0 0 0 1px rgb(148 163 184 / 0.24);
}

.admin-version-button:hover {
  background: rgb(226 232 240);
  color: rgb(51 65 85);
}

.admin-version-dot {
  width: 0.45rem;
  height: 0.45rem;
  flex: 0 0 auto;
  border-radius: 999px;
  background: rgb(148 163 184);
}

.admin-version-dot-update {
  background: rgb(245 158 11);
}

.admin-version-dot-error {
  background: rgb(239 68 68);
}

.admin-version-panel {
  border: 1px solid rgb(226 232 240);
  border-radius: 0.75rem;
  background: rgb(255 255 255 / 0.98);
  color: rgb(15 23 42);
  letter-spacing: 0;
  box-shadow:
    0 18px 42px rgb(15 23 42 / 0.14),
    0 1px 0 rgb(255 255 255 / 0.86) inset;
}

.admin-version-heading {
  font-size: 0.875rem;
  font-weight: 650;
  line-height: 1.25rem;
  color: rgb(15 23 42);
}

.admin-version-refresh {
  display: inline-flex;
  width: 2rem;
  height: 2rem;
  align-items: center;
  justify-content: center;
  border-radius: 0.5rem;
  color: rgb(100 116 139);
  transition: background-color 160ms ease, color 160ms ease;
}

.admin-version-refresh:hover:not(:disabled) {
  background: rgb(241 245 249);
  color: rgb(13 148 136);
}

.admin-version-refresh:disabled {
  cursor: wait;
  opacity: 0.65;
}

.admin-version-number-row {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: center;
  gap: 0.45rem;
}

.admin-version-number {
  min-width: 0;
  overflow-wrap: anywhere;
  color: rgb(15 23 42);
  font-size: 1.8rem;
  font-weight: 800;
  line-height: 1.12;
}

.admin-version-status-badge {
  display: inline-flex;
  width: 1.15rem;
  height: 1.15rem;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  border-radius: 9999px;
  background: rgb(241 245 249);
  color: rgb(100 116 139);
}

.admin-version-status-latest {
  background: rgb(220 252 231);
  color: rgb(22 163 74);
}

.admin-version-status-outdated {
  background: rgb(254 243 199);
  color: rgb(217 119 6);
}

.admin-version-status-error {
  background: rgb(254 226 226);
  color: rgb(220 38 38);
}

.admin-version-subtitle {
  margin-top: 0.45rem;
  font-size: 0.8125rem;
  line-height: 1.2rem;
  color: rgb(100 116 139);
}

.admin-version-subtitle-outdated {
  color: rgb(217 119 6);
}

.admin-version-subtitle-error {
  color: rgb(220 38 38);
}

.admin-version-latest {
  margin-top: 0.55rem;
  font-size: 0.75rem;
  line-height: 1rem;
  color: rgb(100 116 139);
}

.admin-version-error {
  max-width: 100%;
  margin-top: 0.55rem;
  overflow-wrap: anywhere;
  font-size: 0.75rem;
  line-height: 1rem;
  color: rgb(220 38 38);
}

.admin-version-release {
  color: rgb(100 116 139);
}

.admin-version-release:hover:not(:disabled) {
  background: rgb(241 245 249);
  color: rgb(13 148 136);
}

.admin-version-button.admin-version-dark {
  background: rgb(30 41 59 / 0.92);
  color: rgb(203 213 225);
  box-shadow: inset 0 0 0 1px rgb(148 163 184 / 0.12);
}

.admin-version-button.admin-version-dark:hover {
  background: rgb(51 65 85 / 0.95);
  color: rgb(248 250 252);
}

.admin-version-panel.admin-version-dark {
  border-color: rgb(51 65 85 / 0.95);
  background: rgb(30 41 59 / 0.98);
  color: white;
  box-shadow:
    0 18px 42px rgb(2 6 23 / 0.34),
    0 1px 0 rgb(255 255 255 / 0.06) inset;
}

.admin-version-panel.admin-version-dark .admin-version-heading,
.admin-version-panel.admin-version-dark .admin-version-number {
  color: rgb(248 250 252);
}

.admin-version-panel.admin-version-dark .admin-version-refresh,
.admin-version-panel.admin-version-dark .admin-version-subtitle,
.admin-version-panel.admin-version-dark .admin-version-latest,
.admin-version-panel.admin-version-dark .admin-version-release {
  color: rgb(148 163 184);
}

.admin-version-panel.admin-version-dark .admin-version-refresh:hover:not(:disabled) {
  background: rgb(51 65 85 / 0.7);
  color: rgb(226 232 240);
}

.admin-version-panel.admin-version-dark .admin-version-status-badge {
  background: rgb(51 65 85);
  color: rgb(148 163 184);
}

.admin-version-panel.admin-version-dark .admin-version-status-latest {
  background: rgb(22 101 52 / 0.36);
  color: rgb(74 222 128);
}

.admin-version-panel.admin-version-dark .admin-version-status-outdated {
  background: rgb(146 64 14 / 0.36);
  color: rgb(251 191 36);
}

.admin-version-panel.admin-version-dark .admin-version-status-error {
  background: rgb(153 27 27 / 0.38);
  color: rgb(248 113 113);
}

.admin-version-panel.admin-version-dark .admin-version-subtitle-outdated {
  color: rgb(251 191 36);
}

.admin-version-panel.admin-version-dark .admin-version-subtitle-error,
.admin-version-panel.admin-version-dark .admin-version-error {
  color: rgb(248 113 113);
}

.admin-version-panel.admin-version-dark .admin-version-release:hover:not(:disabled) {
  background: rgb(51 65 85 / 0.62);
  color: rgb(226 232 240);
}

@keyframes sidebar-label-fade-in {
  from {
    opacity: 0;
    transform: translateX(-0.25rem);
  }

  to {
    opacity: 1;
    transform: translateX(0);
  }
}

@media (prefers-reduced-motion: reduce) {
  .admin-sidebar,
  .admin-content-shell,
  .admin-sidebar-backdrop,
  .admin-sidebar-brand,
  nav,
  nav a,
  .admin-sidebar button,
  .sidebar-label-fade {
    animation: none !important;
    transition: none !important;
  }
}

@media (min-width: 768px) and (max-width: 1279px) {
  .admin-main {
    padding: 1rem !important;
  }

  header {
    padding-right: 1rem !important;
    padding-left: 1rem !important;
  }
}

@media (max-width: 767px) {
  .admin-sidebar {
    width: min(18rem, calc(100vw - 2rem)) !important;
    max-width: calc(100vw - 0.75rem);
    transform: translateX(calc(-100% - 1rem));
    box-shadow: 0 24px 60px rgb(15 23 42 / 0.28);
  }

  .admin-sidebar-mobile-open {
    transform: translateX(0);
  }

  .admin-sidebar-brand {
    justify-content: flex-start !important;
    padding-right: 0.75rem !important;
    padding-left: 0.85rem !important;
  }

  .admin-sidebar-brand .admin-mobile-close-button {
    margin-left: auto;
  }

  .admin-sidebar nav {
    padding-right: 0.75rem !important;
    padding-left: 0.75rem !important;
  }

  .admin-sidebar .admin-nav-link {
    grid-template-columns: 1.25rem minmax(0, 1fr) !important;
    justify-content: stretch !important;
    padding-right: 0.75rem !important;
    padding-left: 0.75rem !important;
  }

  .admin-sidebar .admin-sidebar-tool-button {
    width: 100%;
    justify-content: flex-start;
    padding-right: 0.75rem;
    padding-left: 0.75rem;
  }

  .admin-sidebar-collapse {
    display: none;
  }

  .admin-content-shell {
    padding-left: 0 !important;
  }

  header {
    padding-right: 0.75rem !important;
    padding-left: 0.75rem !important;
  }

  header h1 {
    max-width: 8rem;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 1.125rem;
  }

  main {
    padding: 0.75rem !important;
  }
}
</style>
