<script setup lang="ts">
import { computed, defineComponent, h, onMounted, ref } from 'vue'
import { useQueryClient } from '@tanstack/vue-query'
import { RefreshCw, Users, WalletCards } from 'lucide-vue-next'
import { listUsers } from '../api/adminUsers'
import { listMailAccounts } from '../api/mailAccounts'
import { listOutlookAccounts } from '../api/outlookAccounts'
import { getSessionItem, setSessionItem } from '../api/session'
import { useAppStore } from '../stores/app'

type ProfileUser = {
  id?: number
  username?: string
  email?: string
  avatar_url?: string
  balance?: number
  role?: string
  status?: string
  created_at?: string
}

const appStore = useAppStore()
const queryClient = useQueryClient()
const dashboardCacheKey = 'dashboard_stats_cache_v1'
const loading = ref(false)
const hasLoaded = ref(false)
const profile = ref<ProfileUser>(readStoredUser())
const mailAccountStats = ref<{ total: number; normal: number; error: number } | null>(null)
const outlookAccountStats = ref<{ total: number; normal: number; error: number } | null>(null)
const userRoleStats = ref<{ total: number; admin: number; regular: number } | null>(null)
const cachedStats = ref<{
  balance: number
  imap: { total: number; normal: number; error: number }
  outlook: { total: number; normal: number; error: number }
  users: { total: number; admin: number; regular: number }
} | null>(readDashboardCache())
const hasDashboardData = computed(() => hasLoaded.value || Boolean(cachedStats.value))

const ImapMailIcon = defineComponent({
  name: 'DashboardImapMailIcon',
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
  name: 'DashboardMicrosoftMailIcon',
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
          'aria-hidden': 'true',
        },
        [
          h('rect', { x: '3.2', y: '3.2', width: '7.8', height: '7.8', rx: '0.8', fill: '#f25022' }),
          h('rect', { x: '13', y: '3.2', width: '7.8', height: '7.8', rx: '0.8', fill: '#7fba00' }),
          h('rect', { x: '3.2', y: '13', width: '7.8', height: '7.8', rx: '0.8', fill: '#00a4ef' }),
          h('rect', { x: '13', y: '13', width: '7.8', height: '7.8', rx: '0.8', fill: '#ffb900' }),
        ]
      )
  },
})

function readStoredUser(): ProfileUser {
  try {
    return JSON.parse(getSessionItem('auth_user') || '{}')
  } catch {
    return {}
  }
}

function readDashboardCache() {
  try {
    const value = JSON.parse(localStorage.getItem(dashboardCacheKey) || 'null')
    if (!value || typeof value !== 'object') return null
    return value
  } catch {
    return null
  }
}

function saveDashboardCache() {
  try {
    const value = {
      balance: Number(profile.value.balance || 0),
      imap: imapStats.value,
      outlook: outlookStats.value,
      users: userStats.value,
      updated_at: Date.now(),
    }
    cachedStats.value = value
    localStorage.setItem(dashboardCacheKey, JSON.stringify(value))
  } catch {
    // Ignore storage quota errors; the fresh in-memory dashboard still renders.
  }
}

function currentUserID() {
  return profile.value.id ? String(profile.value.id) : '1'
}

async function fetchProfile() {
  const response = await fetch('/api/user/profile', {
    headers: {
      'Content-Type': 'application/json',
      'X-User-ID': currentUserID(),
    },
  })
  const result = await response.json().catch(() => null)
  if (!response.ok || result?.code !== 0 || !result.data) return null
  return result.data as ProfileUser
}

function applyProfile(nextProfile: ProfileUser) {
  profile.value = { ...profile.value, ...nextProfile }
  setSessionItem('auth_user', JSON.stringify(profile.value))
  window.dispatchEvent(new Event('storage'))
}

const balanceText = computed(() => {
  const value = hasLoaded.value ? Number(profile.value.balance || 0) : Number(cachedStats.value?.balance || profile.value.balance || 0)
  return `$${Number.isFinite(value) ? value.toFixed(2) : '0.00'}`
})

const imapStats = computed(() => {
  if (!hasLoaded.value && cachedStats.value?.imap) return cachedStats.value.imap
  if (mailAccountStats.value) return mailAccountStats.value
  return { total: 0, normal: 0, error: 0 }
})

const outlookStats = computed(() => {
  if (!hasLoaded.value && cachedStats.value?.outlook) return cachedStats.value.outlook
  if (outlookAccountStats.value) return outlookAccountStats.value
  return { total: 0, normal: 0, error: 0 }
})

const userStats = computed(() => {
  if (!hasLoaded.value && cachedStats.value?.users) return cachedStats.value.users
  if (userRoleStats.value) return userRoleStats.value
  return { total: 0, admin: 0, regular: 0 }
})

async function loadDashboard(force = false) {
  loading.value = true
  try {
    if (force) {
      await queryClient.invalidateQueries({ queryKey: ['dashboard'] })
    }

    const [profileData, outlookResult, imapResult, adminResult, regularResult] = await Promise.all([
      queryClient.fetchQuery({
        queryKey: ['dashboard', 'profile'],
        queryFn: fetchProfile,
        staleTime: 30_000,
      }),
      queryClient.fetchQuery({
        queryKey: ['dashboard', 'outlook-stats'],
        queryFn: () => listOutlookAccounts({ page: 1, page_size: 1 }),
        staleTime: 30_000,
      }),
      queryClient.fetchQuery({
        queryKey: ['dashboard', 'imap-stats'],
        queryFn: () => listMailAccounts({ page: 1, page_size: 1 }),
        staleTime: 30_000,
      }),
      queryClient.fetchQuery({
        queryKey: ['dashboard', 'admin-users'],
        queryFn: () => listUsers({ page: 1, page_size: 1, role: 'admin' }),
        staleTime: 30_000,
      }),
      queryClient.fetchQuery({
        queryKey: ['dashboard', 'regular-users'],
        queryFn: () => listUsers({ page: 1, page_size: 1, role: 'user' }),
        staleTime: 30_000,
      }),
    ])

    if (profileData) {
      applyProfile(profileData)
    }
    outlookAccountStats.value = { total: outlookResult.total, normal: outlookResult.normal, error: outlookResult.error }
    mailAccountStats.value = { total: imapResult.total, normal: imapResult.normal, error: imapResult.error }
    userRoleStats.value = {
      total: adminResult.total + regularResult.total,
      admin: adminResult.total,
      regular: regularResult.total,
    }
    hasLoaded.value = true
    saveDashboardCache()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '加载仪表盘数据失败')
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  if (cachedStats.value) {
    hasLoaded.value = false
  }
  loadDashboard()
})
</script>

<template>
  <div class="space-y-5">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <h2 class="text-xl font-bold text-gray-900 dark:text-white">数据概览</h2>
        <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">账户余额、邮箱账号与系统用户状态。</p>
      </div>
      <button class="btn btn-secondary h-10" type="button" :disabled="loading" @click="loadDashboard(true)">
        <RefreshCw class="h-4 w-4" :class="{ 'animate-spin': loading }" />
        刷新
      </button>
    </div>

    <div class="dashboard-grid">
      <section class="dashboard-card">
        <div class="dashboard-icon dashboard-icon-balance">
          <WalletCards class="h-5 w-5" />
        </div>
        <div class="min-w-0">
          <p class="dashboard-title">账户余额</p>
          <p class="dashboard-value">{{ hasDashboardData ? balanceText : '-' }}</p>
          <p class="dashboard-meta">
            <span>当前账户</span>
          </p>
        </div>
      </section>

      <section class="dashboard-card">
        <div class="dashboard-icon dashboard-icon-microsoft">
          <MicrosoftMailIcon class="h-5 w-5" />
        </div>
        <div class="min-w-0">
          <p class="dashboard-title">微软邮箱</p>
          <p class="dashboard-value">{{ hasDashboardData ? outlookStats.total : '-' }}</p>
          <p class="dashboard-meta">
            <span class="dashboard-normal">{{ hasDashboardData ? outlookStats.normal : '-' }} 正常</span>
            <span v-if="hasDashboardData && outlookStats.error > 0" class="dashboard-error">{{ outlookStats.error }} 错误</span>
          </p>
        </div>
      </section>

      <section class="dashboard-card">
        <div class="dashboard-icon dashboard-icon-imap">
          <ImapMailIcon class="h-5 w-5" />
        </div>
        <div class="min-w-0">
          <p class="dashboard-title">IMAP 邮箱</p>
          <p class="dashboard-value">{{ hasDashboardData ? imapStats.total : '-' }}</p>
          <p class="dashboard-meta">
            <span class="dashboard-normal">{{ hasDashboardData ? imapStats.normal : '-' }} 正常</span>
            <span v-if="hasDashboardData && imapStats.error > 0" class="dashboard-error">{{ imapStats.error }} 错误</span>
          </p>
        </div>
      </section>

      <section class="dashboard-card">
        <div class="dashboard-icon dashboard-icon-users">
          <Users class="h-5 w-5" />
        </div>
        <div class="min-w-0">
          <p class="dashboard-title">系统用户</p>
          <p class="dashboard-value">{{ hasDashboardData ? userStats.total : '-' }}</p>
          <p class="dashboard-meta">
            <span class="dashboard-admin">{{ hasDashboardData ? userStats.admin : '-' }} 管理员</span>
            <span>{{ hasDashboardData ? userStats.regular : '-' }} 用户</span>
          </p>
        </div>
      </section>
    </div>
  </div>
</template>

<style scoped>
.dashboard-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 1rem;
}

.dashboard-card {
  display: grid;
  min-height: 5.9rem;
  grid-template-columns: 2.25rem minmax(0, 1fr);
  align-items: center;
  gap: 0.85rem;
  border-radius: 1rem;
  border: 1px solid rgb(203 213 225 / 0.82);
  background: rgb(255 255 255 / 0.94);
  padding: 1rem;
  box-shadow: 0 14px 30px rgb(15 23 42 / 0.06);
}

.dark .dashboard-card {
  border-color: rgb(51 65 85 / 0.86);
  background: rgb(15 23 42 / 0.78);
  box-shadow: none;
}

.dashboard-title {
  font-size: 0.8125rem;
  font-weight: 700;
  color: rgb(100 116 139);
}

.dark .dashboard-title {
  color: rgb(148 163 184);
}

.dashboard-value {
  margin-top: 0.15rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 1.45rem;
  line-height: 1.1;
  font-weight: 800;
  color: rgb(15 23 42);
}

.dark .dashboard-value {
  color: white;
}

.dashboard-meta {
  margin-top: 0.1rem;
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  gap: 0.45rem;
  font-size: 0.78rem;
  font-weight: 700;
  color: rgb(100 116 139);
}

.dark .dashboard-meta {
  color: rgb(148 163 184);
}

.dashboard-icon {
  display: inline-flex;
  height: 2.25rem;
  width: 2.25rem;
  align-items: center;
  justify-content: center;
  border-radius: 0.65rem;
}

.dashboard-icon-balance {
  background: rgb(20 184 166 / 0.14);
  color: rgb(45 212 191);
}

.dashboard-icon-imap {
  background: rgb(96 165 250 / 0.14);
  color: rgb(96 165 250);
}

.dashboard-icon-microsoft {
  background: rgb(255 255 255 / 0.08);
  color: inherit;
}

.dashboard-icon-users {
  background: rgb(167 139 250 / 0.14);
  color: rgb(167 139 250);
}

.dashboard-normal {
  color: rgb(52 211 153) !important;
}

.dashboard-error {
  color: rgb(251 113 133) !important;
}

.dashboard-admin {
  color: rgb(34 211 238) !important;
}

@media (max-width: 1280px) {
  .dashboard-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 640px) {
  .dashboard-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
