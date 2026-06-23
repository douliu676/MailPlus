<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { ArrowLeft, Copy, Eye, EyeOff, Inbox, Mail, Moon, RefreshCw, Search, Sun, Trash2, X } from 'lucide-vue-next'
import AppLogo from '../components/AppLogo.vue'
import PaginationBar from '../components/PaginationBar.vue'
import { receiveQuickMail, type QuickMailMessage } from '../api/quickMail'
import { authSessionClearedEvent, authSessionClearedStorageKey, clearAuthSession, getAuthToken } from '../api/session'
import { useAppStore } from '../stores/app'
import { useTheme } from '../theme'
import { mailContactEmails } from '../utils/mailContacts'
import { sanitizeMailHtml } from '../utils/sanitizeMailHtml'

type FolderKey = 'inbox' | 'trash'
type MailMode = 'imap' | 'outlook'

const route = useRoute()
const appStore = useAppStore()
const { isDark } = useTheme()
const fallbackMailPageSize = 20
const mailPageSizeOptions = ref<number[]>([fallbackMailPageSize])
const defaultMailPageSize = ref(fallbackMailPageSize)
const quickMailAdminKeyStorageKeys: Record<MailMode, string> = {
  imap: 'quick_mail_imap_admin_key',
  outlook: 'quick_mail_outlook_admin_key',
}
const authAvailable = ref(false)
const quickMailPageSizeStorageKeys: Record<MailMode, string> = {
  imap: 'quick_mail_imap_page_size',
  outlook: 'quick_mail_outlook_page_size',
}

function currentRouteMailMode(): MailMode {
  return route.meta.quickMailMode === 'outlook' || route.path.startsWith('/outlook/') ? 'outlook' : 'imap'
}

function readStoredAdminKey(currentMode: MailMode) {
  if (typeof window === 'undefined') return ''

  try {
    return localStorage.getItem(quickMailAdminKeyStorageKeys[currentMode]) || ''
  } catch {
    return ''
  }
}

function readStoredPageSize(currentMode: MailMode) {
  if (typeof window === 'undefined') return defaultMailPageSize.value

  try {
    const value = Number(localStorage.getItem(quickMailPageSizeStorageKeys[currentMode]))
    return Number.isFinite(value) && mailPageSizeOptions.value.includes(value) ? value : defaultMailPageSize.value
  } catch {
    return defaultMailPageSize.value
  }
}

function writeStoredAdminKey(currentMode: MailMode, value: string) {
  if (typeof window === 'undefined') return

  try {
    localStorage.setItem(quickMailAdminKeyStorageKeys[currentMode], value)
  } catch {
    // 本地存储不可用时跳过，不影响页面使用。
  }
}

function writeStoredPageSize(currentMode: MailMode, value: number) {
  if (typeof window === 'undefined') return

  try {
    const nextValue = mailPageSizeOptions.value.includes(value) ? value : defaultMailPageSize.value
    localStorage.setItem(quickMailPageSizeStorageKeys[currentMode], String(nextValue))
  } catch {
    // 本地存储不可用时跳过，不影响页面使用。
  }
}

const initialMode = currentRouteMailMode()

const form = reactive({
  adminKey: readStoredAdminKey(initialMode),
  email: '',
  limit: 1,
})

const activeFolder = ref<FolderKey>('inbox')
const submitted = ref(false)
const fetching = ref(false)
const keyVisible = ref(false)
const selectedMessage = ref<QuickMailMessage | null>(null)
const lastQueryEmail = ref('')
const messages = ref<QuickMailMessage[]>([])
const mailPage = ref(1)
const mailPageSize = ref(readStoredPageSize(initialMode))
const searchQuery = ref('')
const outlookAliasDomains = new Set(['outlook.com', 'hotmail.com'])
const randomAliasChars = 'abcdefghijklmnopqrstuvwxyz0123456789'

const modeConfigs = {
  imap: {
    title: 'IMAP 收件',
    emailPlaceholder: '请输入邮箱账号',
  },
  outlook: {
    title: 'Outlook 收件',
    emailPlaceholder: '请输入邮箱账号',
  },
} satisfies Record<MailMode, {
  title: string
  emailPlaceholder: string
}>

const mode = computed<MailMode>(() => currentRouteMailMode())
const modeConfig = computed(() => modeConfigs[mode.value])
const siteLogoSrc = computed(() => appStore.siteLogo.value)
const pageTitle = computed(() => `${modeConfig.value.title} - ${appStore.siteName.value}`)
const normalizedEmail = computed(() => form.email.trim())
const normalizedAdminKey = computed(() => form.adminKey.trim())
const normalizedLimit = computed(() => {
  const value = Math.floor(Number(form.limit) || 1)
  return Math.min(100, Math.max(1, value))
})
const emailInvalid = computed(() => submitted.value && !isValidQuickMailEmail(normalizedEmail.value))
const adminKeyInvalid = computed(() => submitted.value && !authAvailable.value && !normalizedAdminKey.value)
const folderMessages = computed(() => messages.value.filter((message) => message.folder === activeFolder.value))
const normalizedSearchQuery = computed(() => searchQuery.value.trim().toLowerCase())
const filteredMessages = computed(() => {
  const keyword = normalizedSearchQuery.value
  if (!keyword) return folderMessages.value

  return folderMessages.value.filter((message) => (
    String(message.subject || '').toLowerCase().includes(keyword)
    || String(message.from || '').toLowerCase().includes(keyword)
    || String(message.to || '').toLowerCase().includes(keyword)
  ))
})
const mailTotalPages = computed(() => Math.max(1, Math.ceil(filteredMessages.value.length / mailPageSize.value)))
const visibleMessages = computed(() => {
  const start = (mailPage.value - 1) * mailPageSize.value
  return filteredMessages.value.slice(start, start + mailPageSize.value)
})
const inboxCount = computed(() => messages.value.filter((message) => message.folder === 'inbox').length)
const trashCount = computed(() => messages.value.filter((message) => message.folder === 'trash').length)
const emptyText = computed(() => {
  if (fetching.value) return '正在收取邮件...'
  if (normalizedSearchQuery.value) return '没有匹配的邮件'
  if (lastQueryEmail.value) return `${lastQueryEmail.value} 暂无邮件`
  return '暂无邮件'
})

const selectedMessagePlainHtml = computed(() => linkifyText(selectedMessage.value?.body || '暂无正文'))
const selectedMessageSafeHtml = computed(() => sanitizeMailHtml(selectedMessage.value?.html || ''))

function escapeHTML(value: string) {
  return value.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#039;')
}

function linkifyText(value: string) {
  return escapeHTML(value || '').replace(/(https?:\/\/[^\s<]+)/g, '<a href="$1" target="_blank" rel="noopener noreferrer">$1</a>').replace(/\n/g, '<br />')
}

function applyStoredQuickMailSettings(currentMode: MailMode) {
  form.adminKey = readStoredAdminKey(currentMode)
  mailPageSize.value = readStoredPageSize(currentMode)
}

function normalizePageSizeOptions(values: unknown, defaultPageSize: number) {
  const options = Array.isArray(values) ? values : [fallbackMailPageSize]
  const result = options
    .map((value) => Math.floor(Number(value) || 0))
    .filter((value) => value > 0)

  if (defaultPageSize > 0) {
    result.push(defaultPageSize)
  }

  return Array.from(new Set(result)).sort((a, b) => a - b)
}

function applyPublicPageSizeSettings(settings = appStore.cachedPublicSettings.value) {
  const defaultSize = Math.floor(Number(settings?.table_default_page_size) || fallbackMailPageSize)
  defaultMailPageSize.value = defaultSize > 0 ? defaultSize : fallbackMailPageSize
  mailPageSizeOptions.value = normalizePageSizeOptions(settings?.table_page_size_options, defaultMailPageSize.value)
  mailPageSize.value = readStoredPageSize(mode.value)
}

function refreshAuthAvailable() {
  authAvailable.value = Boolean(getAuthToken())
}

function handleAuthSessionCleared() {
  clearAuthSession(false)
  authAvailable.value = false
}

function handleAuthStorage(event: StorageEvent) {
  if (event.key === authSessionClearedStorageKey) {
    handleAuthSessionCleared()
    return
  }
  refreshAuthAvailable()
}

function isValidQuickMailEmail(value: string) {
  return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value.trim())
}

onMounted(async () => {
  refreshAuthAvailable()
  window.addEventListener('storage', handleAuthStorage)
  window.addEventListener(authSessionClearedEvent, handleAuthSessionCleared)
  applyPublicPageSizeSettings()
  const settings = await appStore.fetchPublicSettings(true)
  applyPublicPageSizeSettings(settings || appStore.cachedPublicSettings.value)
})

onBeforeUnmount(() => {
  window.removeEventListener('storage', handleAuthStorage)
  window.removeEventListener(authSessionClearedEvent, handleAuthSessionCleared)
})

watch(pageTitle, (title) => {
  document.title = title
}, { immediate: true })

watch(mode, (currentMode) => {
  applyStoredQuickMailSettings(currentMode)
  activeFolder.value = 'inbox'
  selectedMessage.value = null
  lastQueryEmail.value = ''
  submitted.value = false
  messages.value = []
  searchQuery.value = ''
  mailPage.value = 1
})

watch([activeFolder, mailPageSize, normalizedSearchQuery], () => {
  mailPage.value = 1
})

watch(mailTotalPages, (pages) => {
  if (mailPage.value > pages) {
    mailPage.value = pages
  }
})

async function handleFetch() {
  submitted.value = true
  form.limit = normalizedLimit.value
  saveQuickMailAdminKey()
  refreshAuthAvailable()

  if (!authAvailable.value && !normalizedAdminKey.value) {
    appStore.showError('请输入秘钥')
    return
  }

  if (!isValidQuickMailEmail(normalizedEmail.value)) {
    appStore.showError('输入有误')
    return
  }

  fetching.value = true
  selectedMessage.value = null
  try {
    const result = await receiveQuickMail(mode.value, {
      email: normalizedEmail.value,
      limit: normalizedLimit.value,
      admin_key: normalizedAdminKey.value,
    })
    fetching.value = false
    lastQueryEmail.value = normalizedEmail.value
    messages.value = [...(result.inbox || []), ...(result.trash || [])].map((message) => ({
      ...message,
      folder: message.folder === 'trash' ? 'trash' : 'inbox',
      body: message.body || message.body_preview || '',
      html: message.html || '',
    }))
    mailPage.value = 1
    appStore.showSuccess('收取完成')
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '收取邮件失败')
  } finally {
    fetching.value = false
  }
}

async function copyRandomOutlookAlias() {
  const email = normalizedEmail.value
  const [localPart, domainPart, extraPart] = email.split('@')
  const domain = domainPart?.toLowerCase()

  if (!localPart || !domain || extraPart || !outlookAliasDomains.has(domain)) {
    appStore.showError('请输入有效的 Outlook/Hotmail 邮箱')
    return
  }

  const length = 5 + Math.floor(Math.random() * 3)
  let suffix = ''
  for (let index = 0; index < length; index += 1) {
    suffix += randomAliasChars[Math.floor(Math.random() * randomAliasChars.length)]
  }

  const aliasEmail = `${localPart}+${suffix}@${domain}`
  try {
    await navigator.clipboard.writeText(aliasEmail)
    appStore.showSuccess(`已复制：${aliasEmail}`)
  } catch {
    appStore.showError('复制失败')
  }
}

function setFolder(folder: FolderKey) {
  activeFolder.value = folder
  selectedMessage.value = null
  mailPage.value = 1
}

function selectMessage(message: QuickMailMessage) {
  selectedMessage.value = message
}

function closeDetail() {
  selectedMessage.value = null
}

function setMailPage(page: number) {
  mailPage.value = Math.max(1, Math.min(Math.floor(Number(page) || 1), mailTotalPages.value))
}

function setMailPageSize(size: number) {
  const value = Math.floor(Number(size) || defaultMailPageSize.value)
  const nextSize = mailPageSizeOptions.value.includes(value) ? value : defaultMailPageSize.value
  mailPageSize.value = nextSize
  writeStoredPageSize(mode.value, nextSize)
  mailPage.value = 1
}

function saveQuickMailAdminKey(event?: Event) {
  const target = event?.target instanceof HTMLInputElement ? event.target : null
  writeStoredAdminKey(mode.value, target?.value ?? form.adminKey)
}

function toggleKeyVisible() {
  keyVisible.value = !keyVisible.value
}
</script>

<template>
  <div class="quick-mail-page">
    <div class="quick-mail-shell">
      <header class="quick-mail-topbar">
        <div class="quick-mail-brand">
          <span class="quick-mail-logo">
            <Mail class="quick-mail-logo-fallback h-5 w-5" />
            <AppLogo :src="siteLogoSrc" />
          </span>
          <span class="quick-mail-brand-copy">
            <strong>{{ appStore.siteName.value }}</strong>
            <span>{{ appStore.siteSubtitle.value }}</span>
          </span>
        </div>

        <label class="quick-mail-top-search search-clear-field">
          <Search class="h-4 w-4" />
          <input v-model="searchQuery" class="search-clear-input" type="search" placeholder="搜索标题 / 发件人 / 收件人" autocomplete="off" />
          <button v-if="searchQuery" class="search-clear-button" type="button" title="清空搜索" aria-label="清空搜索" @click="searchQuery = ''">
            <X class="h-3.5 w-3.5" />
          </button>
        </label>

        <button
          class="quick-mail-theme-button"
          type="button"
          :title="isDark ? '切换亮色模式' : '切换暗色模式'"
          @click="isDark = !isDark"
        >
          <Sun v-if="isDark" class="h-5 w-5" />
          <Moon v-else class="h-5 w-5" />
        </button>
      </header>

      <form class="quick-mail-query-card" :class="{ 'is-outlook': mode === 'outlook' }" @submit.prevent="handleFetch">
        <label class="quick-mail-field" :class="{ 'is-invalid': adminKeyInvalid }">
          <span>秘钥</span>
          <div class="quick-mail-secret-control">
            <input
              v-model="form.adminKey"
              class="input quick-mail-secret-input"
              :type="keyVisible ? 'text' : 'password'"
              autocomplete="off"
              :placeholder="authAvailable ? '已登录可留空' : '请输入秘钥'"
              @input="saveQuickMailAdminKey"
              @change="saveQuickMailAdminKey"
            />
            <button
              class="quick-mail-secret-toggle"
              type="button"
              :title="keyVisible ? '隐藏秘钥' : '显示秘钥'"
              @click="toggleKeyVisible"
            >
              <EyeOff v-if="keyVisible" class="h-4 w-4" />
              <Eye v-else class="h-4 w-4" />
            </button>
          </div>
        </label>

        <label class="quick-mail-field quick-mail-email-field" :class="{ 'is-invalid': emailInvalid }">
          <span>邮箱账号</span>
          <input v-model.trim="form.email" class="input" type="email" :placeholder="modeConfig.emailPlaceholder" autocomplete="off" />
        </label>

        <label class="quick-mail-field quick-mail-limit-field">
          <span>获取几封邮件</span>
          <input v-model.number="form.limit" class="input" type="number" min="1" max="100" step="1" />
        </label>

        <button v-if="mode === 'outlook'" class="btn btn-secondary quick-mail-random-alias-button" type="button" @click="copyRandomOutlookAlias">
          <Copy class="h-4 w-4" />
          <span>复制随机邮箱</span>
        </button>

        <button class="btn btn-primary quick-mail-fetch-button" type="submit" :disabled="fetching">
          <RefreshCw class="h-4 w-4" :class="{ 'animate-spin': fetching }" />
          <span>{{ fetching ? '获取中...' : '获取邮件' }}</span>
        </button>
      </form>

      <section class="quick-mail-workspace">
        <aside class="quick-mail-folder-card">
          <div class="quick-mail-folder-list">
            <button class="quick-mail-folder-button" :class="{ active: activeFolder === 'inbox' }" type="button" @click="setFolder('inbox')">
              <Inbox class="h-4 w-4" />
              <span>收件箱</span>
              <em>{{ inboxCount }}</em>
            </button>
            <button class="quick-mail-folder-button" :class="{ active: activeFolder === 'trash' }" type="button" @click="setFolder('trash')">
              <Trash2 class="h-4 w-4" />
              <span>垃圾箱</span>
              <em>{{ trashCount }}</em>
            </button>
          </div>
        </aside>

        <section class="quick-mail-display-card">
          <div v-if="!selectedMessage" class="quick-mail-table-wrap">
            <table class="quick-mail-table">
              <thead>
                <tr>
                  <th class="quick-mail-col-subject">标题</th>
                  <th>收件人</th>
                  <th>发件人</th>
                  <th class="quick-mail-col-time">时间</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="message in visibleMessages" :key="message.id" @click="selectMessage(message)">
                  <td class="quick-mail-subject-cell" :title="message.subject">{{ message.subject || '无标题' }}</td>
                  <td :title="mailContactEmails(message.to)">{{ mailContactEmails(message.to) || '-' }}</td>
                  <td :title="mailContactEmails(message.from)">{{ mailContactEmails(message.from) || '-' }}</td>
                  <td :title="message.time">{{ message.time || '-' }}</td>
                </tr>
                <tr v-if="visibleMessages.length === 0" class="quick-mail-empty-row">
                  <td colspan="4">
                    <div class="quick-mail-empty">
                      <Inbox class="h-5 w-5" />
                      <span>{{ emptyText }}</span>
                    </div>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>

          <div v-if="!selectedMessage" class="quick-mail-pagination">
            <PaginationBar
              :page="mailPage"
              :pages="mailTotalPages"
              :page-size="mailPageSize"
              :page-size-options="mailPageSizeOptions"
              :total="filteredMessages.length"
              @page-change="setMailPage"
              @page-size-change="setMailPageSize"
            />
          </div>

          <div v-else class="quick-mail-detail-area">
            <div class="quick-mail-detail-header">
              <button class="quick-mail-back-button" type="button" @click="closeDetail">
                <ArrowLeft class="h-4 w-4" />
                <span>返回列表</span>
              </button>
            </div>
            <article class="quick-mail-detail-content">
              <h2>{{ selectedMessage.subject || '无标题' }}</h2>
              <div class="quick-mail-detail-meta">
                <span>发件人：{{ selectedMessage.from || '-' }}</span>
                <span>收件人：{{ selectedMessage.to || '-' }}</span>
                <span>时间：{{ selectedMessage.time || '-' }}</span>
                <span>所属：{{ selectedMessage.folder === 'trash' ? '垃圾箱' : '收件箱' }}</span>
              </div>
              <div v-if="selectedMessage.html" class="quick-mail-detail-body" v-html="selectedMessageSafeHtml"></div>
              <div v-else class="quick-mail-detail-body quick-mail-detail-plain" v-html="selectedMessagePlainHtml"></div>
            </article>
          </div>
        </section>
      </section>
    </div>
  </div>
</template>

<style scoped>
.quick-mail-page {
  position: relative;
  display: flex;
  height: 100vh;
  height: 100dvh;
  min-height: 0;
  overflow: hidden;
  background: #f9fafb;
  color: #111827;
}

.quick-mail-page::before {
  position: absolute;
  inset: 0;
  z-index: 0;
  pointer-events: none;
  content: "";
  background:
    radial-gradient(at 40% 20%, rgb(20 184 166 / 0.12) 0, transparent 50%),
    radial-gradient(at 80% 0%, rgb(6 182 212 / 0.08) 0, transparent 50%),
    radial-gradient(at 0% 50%, rgb(20 184 166 / 0.08) 0, transparent 50%);
}

.dark .quick-mail-page {
  background: #020617;
  color: #f8fafc;
}

.quick-mail-shell {
  position: relative;
  z-index: 1;
  display: flex;
  height: 100%;
  min-height: 0;
  flex-direction: column;
  width: 100%;
  overflow: hidden;
  padding: 1.25rem 1rem 1rem;
  box-sizing: border-box;
}

.quick-mail-topbar {
  display: grid;
  flex-shrink: 0;
  grid-template-columns: minmax(0, 1fr) minmax(18rem, 32rem) minmax(0, 1fr);
  min-height: 4rem;
  align-items: center;
  gap: 1rem;
  border: 1px solid rgb(229 231 235);
  border-radius: 0.75rem;
  background: rgb(255 255 255 / 0.9);
  padding: 0.85rem 1rem;
  box-shadow: 0 1px 3px rgb(15 23 42 / 0.06), 0 10px 30px rgb(15 23 42 / 0.05);
  backdrop-filter: blur(14px);
}

.dark .quick-mail-topbar {
  border-color: rgb(51 65 85 / 0.72);
  background: rgb(15 23 42 / 0.86);
  box-shadow: 0 16px 36px rgb(0 0 0 / 0.28);
}

.quick-mail-brand {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 0.75rem;
}

.quick-mail-logo {
  position: relative;
  display: inline-flex;
  width: 2.25rem;
  height: 2.25rem;
  flex: 0 0 2.25rem;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  border-radius: 0.5rem;
  background: #f0fdfa;
  color: #0d9488;
}

.dark .quick-mail-logo {
  background: rgb(20 184 166 / 0.14);
  color: #5eead4;
}

.quick-mail-logo-fallback {
  position: absolute;
  inset: auto;
  z-index: 0;
}

.quick-mail-logo :deep(.app-logo-shell) {
  position: relative;
  z-index: 1;
}

.quick-mail-brand-copy {
  display: grid;
  min-width: 0;
  gap: 0.08rem;
}

.quick-mail-brand-copy strong,
.quick-mail-brand-copy span {
  max-width: 18rem;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.quick-mail-brand-copy strong {
  color: #111827;
  font-size: 0.95rem;
  font-weight: 700;
  line-height: 1.2;
}

.dark .quick-mail-brand-copy strong {
  color: #ffffff;
}

.quick-mail-brand-copy span {
  color: #64748b;
  font-size: 0.75rem;
  font-weight: 500;
}

.dark .quick-mail-brand-copy span {
  color: #94a3b8;
}

.quick-mail-theme-button {
  display: inline-flex;
  width: 2.5rem;
  height: 2.5rem;
  flex: 0 0 2.5rem;
  align-items: center;
  justify-content: center;
  justify-self: end;
  border: 1px solid rgb(229 231 235);
  border-radius: 0.75rem;
  background: #ffffff;
  color: #64748b;
  transition: border-color 0.16s ease, background-color 0.16s ease, color 0.16s ease;
}

.quick-mail-theme-button:hover {
  border-color: #14b8a6;
  background: #f0fdfa;
  color: #0d9488;
}

.dark .quick-mail-theme-button {
  border-color: rgb(71 85 105);
  background: rgb(30 41 59);
  color: #cbd5e1;
}

.dark .quick-mail-theme-button:hover {
  border-color: #2dd4bf;
  background: rgb(20 184 166 / 0.14);
  color: #5eead4;
}

.quick-mail-top-search {
  display: grid;
  width: 100%;
  height: 2.5rem;
  grid-template-columns: 1rem minmax(0, 1fr);
  align-items: center;
  justify-self: center;
  gap: 0.45rem;
  border: 1px solid rgb(229 231 235);
  border-radius: 0.75rem;
  background: #ffffff;
  padding: 0 0.8rem;
  color: #94a3b8;
  transition: border-color 0.16s ease, box-shadow 0.16s ease;
}

.quick-mail-top-search:focus-within {
  border-color: #14b8a6;
  box-shadow: 0 0 0 2px rgb(20 184 166 / 0.3);
}

.dark .quick-mail-top-search {
  border-color: rgb(71 85 105);
  background: rgb(15 23 42);
  color: #94a3b8;
}

.quick-mail-top-search input {
  min-width: 0;
  border: 0;
  background: transparent;
  color: #64748b;
  font-size: 0.82rem;
  font-weight: 700;
  outline: none;
}

.dark .quick-mail-top-search input {
  color: #cbd5e1;
}

.quick-mail-query-card,
.quick-mail-folder-card,
.quick-mail-display-card {
  border: 1px solid rgb(229 231 235);
  border-radius: 0.75rem;
  background: rgb(255 255 255 / 0.92);
  box-shadow: 0 1px 3px rgb(15 23 42 / 0.06), 0 10px 30px rgb(15 23 42 / 0.05);
  backdrop-filter: blur(14px);
}

.dark .quick-mail-query-card,
.dark .quick-mail-folder-card,
.dark .quick-mail-display-card {
  border-color: rgb(51 65 85 / 0.72);
  background: rgb(30 41 59 / 0.5);
  box-shadow: 0 16px 36px rgb(0 0 0 / 0.24);
}

.quick-mail-query-card {
  display: grid;
  flex-shrink: 0;
  grid-template-columns: minmax(14rem, 20rem) minmax(16rem, 25rem) minmax(7.5rem, 9rem) 9rem;
  gap: 0.9rem;
  align-items: end;
  justify-content: start;
  margin-top: 1rem;
  padding: 1rem;
}

.quick-mail-query-card.is-outlook {
  grid-template-columns: minmax(14rem, 20rem) minmax(16rem, 25rem) minmax(7.5rem, 9rem) 10rem 9rem;
}

.quick-mail-field {
  display: grid;
  min-width: 0;
  gap: 0.45rem;
}

.quick-mail-field > span {
  color: #374151;
  font-size: 0.82rem;
  font-weight: 700;
}

.dark .quick-mail-field > span {
  color: #cbd5e1;
}

.quick-mail-field.is-invalid :deep(.input),
.quick-mail-field.is-invalid .quick-mail-secret-control {
  border-color: #ef4444;
}

.quick-mail-secret-control {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 2.75rem;
  overflow: hidden;
  border: 1px solid rgb(229 231 235);
  border-radius: 0.75rem;
  background: #ffffff;
  transition: border-color 0.16s ease, box-shadow 0.16s ease;
}

.dark .quick-mail-secret-control {
  border-color: rgb(71 85 105);
  background: rgb(30 41 59);
}

.quick-mail-secret-control:focus-within {
  border-color: #14b8a6;
  box-shadow: 0 0 0 2px rgb(20 184 166 / 0.3);
}

.quick-mail-secret-input {
  border: 0 !important;
  border-radius: 0 !important;
  background: transparent !important;
  box-shadow: none !important;
}

.quick-mail-secret-toggle {
  display: inline-flex;
  width: 2.75rem;
  align-items: center;
  justify-content: center;
  color: #64748b;
  transition: background-color 0.16s ease, color 0.16s ease;
}

.quick-mail-secret-toggle:hover {
  background: #f1f5f9;
  color: #0d9488;
}

.dark .quick-mail-secret-toggle {
  color: #94a3b8;
}

.dark .quick-mail-secret-toggle:hover {
  background: rgb(51 65 85 / 0.72);
  color: #5eead4;
}

.quick-mail-limit-field :deep(.input) {
  text-align: center;
}

.quick-mail-fetch-button {
  width: 9rem;
  height: 2.65rem;
  white-space: nowrap;
}

.quick-mail-random-alias-button {
  width: 10rem;
  height: 2.65rem;
  white-space: nowrap;
}

.quick-mail-workspace {
  display: grid;
  flex: 1;
  grid-template-columns: 15rem minmax(0, 1fr);
  gap: 1rem;
  min-height: 0;
  margin-top: 1rem;
  overflow: hidden;
}

.quick-mail-folder-card {
  align-self: stretch;
  min-height: 0;
  overflow: auto;
  padding: 1rem;
}

.quick-mail-folder-list {
  display: grid;
  gap: 0.6rem;
}

.quick-mail-folder-button {
  display: grid;
  grid-template-columns: 1.1rem minmax(0, 1fr) auto;
  min-height: 2.75rem;
  align-items: center;
  gap: 0.6rem;
  border: 1px solid rgb(229 231 235);
  border-radius: 0.75rem;
  background: #ffffff;
  padding: 0 0.8rem;
  color: #4b5563;
  font-size: 0.88rem;
  font-weight: 700;
  text-align: left;
  transition: border-color 0.16s ease, background-color 0.16s ease, color 0.16s ease;
}

.quick-mail-folder-button span {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.quick-mail-folder-button em {
  min-width: 1.55rem;
  border-radius: 9999px;
  background: #f3f4f6;
  padding: 0.1rem 0.42rem;
  color: #64748b;
  font-size: 0.74rem;
  font-style: normal;
  text-align: center;
}

.quick-mail-folder-button:hover,
.quick-mail-folder-button.active {
  border-color: #99f6e4;
  background: #f0fdfa;
  color: #0f766e;
}

.dark .quick-mail-folder-button {
  border-color: rgb(71 85 105);
  background: rgb(15 23 42);
  color: #cbd5e1;
}

.dark .quick-mail-folder-button em {
  background: rgb(30 41 59);
  color: #94a3b8;
}

.dark .quick-mail-folder-button:hover,
.dark .quick-mail-folder-button.active {
  border-color: #2dd4bf;
  background: rgb(20 184 166 / 0.14);
  color: #5eead4;
}

.quick-mail-display-card {
  display: flex;
  min-width: 0;
  min-height: 0;
  flex-direction: column;
  overflow: hidden;
}

.quick-mail-table-wrap {
  --quick-mail-table-divider: rgb(148 163 184 / 0.12);
  min-width: 0;
  min-height: 0;
  flex: 1;
  overflow-x: auto;
  overflow-y: auto;
}

.dark .quick-mail-table-wrap {
  --quick-mail-table-divider: rgb(148 163 184 / 0.18);
}

.quick-mail-table {
  width: max(100%, 78rem);
  min-width: 78rem;
  border-collapse: separate;
  border-spacing: 0;
  table-layout: fixed;
  text-align: left;
  font-size: 0.8125rem;
}

.quick-mail-table th,
.quick-mail-table td {
  height: 3.05rem;
  padding: 0 0.9rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.quick-mail-table th:not(:last-child) {
  border-right: 1px solid var(--quick-mail-table-divider);
}

.quick-mail-table th {
  position: sticky;
  top: 0;
  z-index: 2;
  border-bottom: 1px solid rgb(229 231 235);
  background: #f9fafb;
  color: #64748b;
  font-size: 0.78rem;
  font-weight: 700;
}

.dark .quick-mail-table th {
  border-bottom-color: rgb(51 65 85 / 0.72);
  background: rgb(15 23 42 / 0.72);
  color: #cbd5e1;
}

.quick-mail-table td {
  color: #6b7280;
  font-weight: 700;
}

.dark .quick-mail-table td {
  color: #94a3b8;
}

.quick-mail-table tbody tr:not(:has(.quick-mail-empty)) {
  cursor: pointer;
  transition: background-color 0.16s ease;
}

.quick-mail-table .quick-mail-empty-row td {
  border-bottom: 0;
}

.quick-mail-table tbody tr:not(:has(.quick-mail-empty)):hover {
  background: #f8fafc;
}

.dark .quick-mail-table tbody tr:not(:has(.quick-mail-empty)):hover {
  background: rgb(51 65 85 / 0.36);
}

.quick-mail-col-subject {
  width: 42%;
}

.quick-mail-col-time {
  width: 12rem;
}

.quick-mail-subject-cell {
  color: #374151 !important;
}

.dark .quick-mail-subject-cell {
  color: #e2e8f0 !important;
}

.quick-mail-empty {
  display: flex;
  min-height: 16rem;
  align-items: center;
  justify-content: center;
  gap: 0.55rem;
  color: #94a3b8;
  font-size: 0.88rem;
  font-weight: 800;
}

.quick-mail-pagination {
  flex-shrink: 0;
  border-top: 1px solid rgb(229 231 235);
  padding: 0.85rem 1rem;
}

.dark .quick-mail-pagination {
  border-top-color: rgb(51 65 85 / 0.72);
}

.quick-mail-detail-area {
  display: flex;
  min-height: 0;
  flex: 1;
  flex-direction: column;
  overflow: hidden;
}

.quick-mail-detail-header {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  border-bottom: 1px solid rgb(229 231 235);
  padding: 1rem;
}

.dark .quick-mail-detail-header {
  border-bottom-color: rgb(51 65 85 / 0.72);
}

.quick-mail-back-button {
  display: inline-flex;
  height: 2.35rem;
  align-items: center;
  justify-content: center;
  gap: 0.45rem;
  border: 1px solid rgb(229 231 235);
  border-radius: 0.75rem;
  background: #ffffff;
  padding: 0 0.8rem;
  color: #4b5563;
  font-size: 0.84rem;
  font-weight: 800;
  transition: border-color 0.16s ease, background-color 0.16s ease, color 0.16s ease;
}

.quick-mail-back-button:hover {
  border-color: #99f6e4;
  background: #f0fdfa;
  color: #0f766e;
}

.dark .quick-mail-back-button {
  border-color: rgb(71 85 105);
  background: rgb(15 23 42);
  color: #cbd5e1;
}

.dark .quick-mail-back-button:hover {
  border-color: #2dd4bf;
  background: rgb(20 184 166 / 0.14);
  color: #5eead4;
}

.quick-mail-detail-content {
  display: flex;
  min-height: 0;
  flex: 1;
  flex-direction: column;
  overflow: hidden;
  padding: 1rem;
}

.quick-mail-detail-content h2 {
  flex-shrink: 0;
  margin: 0;
  color: #111827;
  font-size: 1rem;
  font-weight: 800;
}

.dark .quick-mail-detail-content h2 {
  color: #ffffff;
}

.quick-mail-detail-meta {
  display: flex;
  flex-shrink: 0;
  flex-wrap: wrap;
  gap: 0.45rem 1rem;
  margin-top: 0.7rem;
  color: #64748b;
  font-size: 0.8rem;
  font-weight: 700;
}

.dark .quick-mail-detail-meta {
  color: #94a3b8;
}

.quick-mail-detail-body {
  flex: 1 1 auto;
  width: 100%;
  max-width: 100%;
  min-height: 0;
  margin-top: 1rem;
  overflow: auto;
  overscroll-behavior: contain;
  border: 1px solid rgb(226 232 240);
  border-radius: 0.75rem;
  background: rgb(248 250 252);
  padding: 1.2rem;
  box-sizing: border-box;
  color: rgb(31 41 55);
}

.quick-mail-detail-body :deep(img),
.quick-mail-detail-body :deep(video),
.quick-mail-detail-body :deep(canvas),
.quick-mail-detail-body :deep(table) {
  max-width: 100% !important;
}

.quick-mail-detail-body :deep(img),
.quick-mail-detail-body :deep(video),
.quick-mail-detail-body :deep(canvas) {
  height: auto !important;
}

.quick-mail-detail-body :deep(pre),
.quick-mail-detail-body :deep(code),
.quick-mail-detail-body :deep(td),
.quick-mail-detail-body :deep(th) {
  overflow-wrap: anywhere;
}

.quick-mail-detail-body :deep(a),
.quick-mail-detail-plain :deep(a) {
  color: rgb(37 99 235);
  text-decoration: underline;
  text-underline-offset: 2px;
  overflow-wrap: anywhere;
}

.dark .quick-mail-detail-body {
  border-color: rgb(51 65 85);
  background: rgb(15 23 42 / 0.45);
  color: rgb(226 232 240);
}

.dark .quick-mail-detail-body :deep(a),
.dark .quick-mail-detail-plain :deep(a) {
  color: rgb(94 234 212);
}

.quick-mail-detail-plain {
  font-family: inherit;
  line-height: 1.65;
}

.quick-mail-detail-empty {
  display: flex;
  min-height: 8rem;
  align-items: center;
  justify-content: center;
  gap: 0.55rem;
  color: #94a3b8;
  font-size: 0.88rem;
  font-weight: 800;
}

@media (max-width: 1120px) {
  .quick-mail-query-card,
  .quick-mail-query-card.is-outlook {
    grid-template-columns: minmax(14rem, 20rem) minmax(16rem, 25rem);
  }

  .quick-mail-fetch-button {
    width: 9rem;
  }

  .quick-mail-random-alias-button {
    width: 10rem;
  }

  .quick-mail-workspace {
    grid-template-columns: minmax(0, 1fr);
    grid-template-rows: auto minmax(0, 1fr);
  }

  .quick-mail-folder-card {
    min-height: 0;
  }

  .quick-mail-folder-list {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 720px) {
  .quick-mail-shell {
    padding: 0.75rem;
  }

  .quick-mail-topbar {
    grid-template-columns: minmax(0, 1fr) auto;
    min-height: 4rem;
    gap: 0.75rem;
    padding: 0.75rem;
  }

  .quick-mail-top-search {
    grid-column: 1 / -1;
  }

  .quick-mail-logo {
    width: 2.25rem;
    height: 2.25rem;
    flex-basis: 2.25rem;
  }

  .quick-mail-brand-copy strong,
  .quick-mail-brand-copy span {
    max-width: calc(100vw - 7rem);
  }

  .quick-mail-brand-copy strong {
    font-size: 0.95rem;
  }

  .quick-mail-query-card {
    grid-template-columns: minmax(0, 1fr);
    padding: 0.85rem;
  }

  .quick-mail-query-card.is-outlook {
    grid-template-columns: minmax(0, 1fr);
  }

  .quick-mail-random-alias-button,
  .quick-mail-fetch-button {
    width: 100%;
  }
}

@media (max-width: 480px) {
  .quick-mail-folder-list {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
