<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { Copy, Inbox, Loader2, Moon, RefreshCw, Sun, XCircle } from 'lucide-vue-next'
import AppLogo from '../components/AppLogo.vue'
import { getPublicMailInfo, getPublicMailMessages, getPublicMailPlain, PublicMailApiError, type PublicMailInfo, type PublicMailMessage } from '../api/publicMail'
import { useAppStore } from '../stores/app'
import { useTheme } from '../theme'
import { copyToClipboard } from '../utils/clipboard'
import { sanitizeMailHtml } from '../utils/sanitizeMailHtml'

const route = useRoute()
const appStore = useAppStore()
const { isDark } = useTheme()
const cardKey = computed(() => String(route.params.cardKey || '').trim())
const info = ref<PublicMailInfo | null>(null)
const currentMessage = ref<PublicMailMessage | null>(null)
const emailInput = ref('')
const loading = ref(false)
const fetching = ref(false)
const pageError = ref('')
const fetchError = ref('')
const autoFetchText = ref('')
const cooldown = ref(0)
let cooldownTimer: number | undefined

const siteLogoSrc = computed(() => appStore.siteLogo.value)
const pageTitle = computed(() => `API取件 - ${appStore.siteName.value}`)
const autoFetchMode = computed(() => route.meta.autoFetch === true)
const routeEmail = computed(() => routeParamString(route.params.email).trim())
const targetEmail = computed(() => info.value?.bound_email || emailInput.value.trim())
const canFetch = computed(() => Boolean(info.value && info.value.remaining > 0 && targetEmail.value && !fetching.value && cooldown.value <= 0))
const remainingText = computed(() => {
  if (!info.value) return ''
  return `剩余 ${info.value.remaining}/${info.value.usage_limit} 次`
})
const errorTitle = computed(() => {
  if (pageError.value.includes('不存在')) return '此卡密不存在'
  return pageError.value || '无法打开取件页'
})
const errorDescription = computed(() => {
  if (pageError.value.includes('不存在')) return '请检查卡密是否正确，或联系管理员获取有效卡密'
  if (pageError.value.includes('不能为空')) return '请检查链接中的卡密是否完整'
  return '请稍后重试，或联系管理员处理'
})
const mailBodyText = computed(() => {
  if (!currentMessage.value) return ''
  return currentMessage.value.body || currentMessage.value.body_preview || ''
})
const mailHtmlSrcdoc = computed(() => {
  const html = sanitizeMailHtml(currentMessage.value?.html || '').trim()
  if (!html) return ''
  return `<!doctype html><html><head><meta charset="utf-8"><base target="_blank"><style>body{margin:0;background:#fff;color:#111827;font:14px/1.65 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;word-break:break-word}img{max-width:100%;height:auto}a{color:#0f766e}</style></head><body>${html}</body></html>`
})
const emptyMessage = computed(() => {
  if (fetching.value) return '正在收取邮件...'
  if (fetchError.value) return fetchError.value
  if (info.value && info.value.remaining <= 0) return '卡密使用次数已用完'
  if (autoFetchMode.value) return '暂无邮件...'
  return '点击获取后显示邮件'
})

onMounted(() => {
  void appStore.fetchPublicSettings()
  void loadInfo()
})

onBeforeUnmount(() => {
  window.clearInterval(cooldownTimer)
})

watch(pageTitle, (title) => {
  document.title = title
}, { immediate: true })

watch(() => [cardKey.value, routeEmail.value, autoFetchMode.value], () => {
  void loadInfo()
})

async function loadInfo() {
  if (!cardKey.value) {
    pageError.value = '卡密不能为空'
    return
  }
  loading.value = true
  pageError.value = ''
  fetchError.value = ''
  autoFetchText.value = ''
  currentMessage.value = null
  window.clearInterval(cooldownTimer)
  cooldown.value = 0
  try {
    if (autoFetchMode.value) {
      info.value = null
      autoFetchText.value = await getPublicMailPlain(cardKey.value, routeEmail.value || undefined)
      return
    }
    info.value = await getPublicMailInfo(cardKey.value)
    emailInput.value = ''
    const autoError = prepareAutoFetch()
    if (autoError) {
      pageError.value = autoError
      return
    }
  } catch (error) {
    info.value = null
    if (autoFetchMode.value) {
      fetchError.value = error instanceof Error ? error.message : '获取邮件失败'
    } else {
      pageError.value = error instanceof Error ? error.message : '卡密查询失败'
    }
  } finally {
    loading.value = false
  }
}

async function fetchMessages() {
  if (!info.value || info.value.remaining <= 0 || !targetEmail.value || fetching.value) return
  fetching.value = true
  fetchError.value = ''
  try {
    const response = await getPublicMailMessages(cardKey.value, info.value.has_bound_email ? undefined : targetEmail.value)
    currentMessage.value = response.message_item || response.messages[0] || null
    info.value = {
      ...info.value,
      used_count: response.used_count,
      remaining: response.remaining,
    }
    if (!autoFetchMode.value) {
      showFetchToast(response)
    }
    startCooldown(response.cooldown_seconds || 40)
  } catch (error) {
    const message = error instanceof Error ? error.message : '获取邮件失败'
    fetchError.value = message
    if (!autoFetchMode.value) {
      appStore.showError(message)
    }
    if (error instanceof PublicMailApiError) {
      const wait = Number(error.data.wait_seconds) || 0
      if (wait > 0) startCooldown(wait)
    }
  } finally {
    fetching.value = false
  }
}

function prepareAutoFetch() {
  if (!autoFetchMode.value || !info.value) return ''
  const email = routeEmail.value
  if (info.value.has_bound_email) {
    if (email && normalizeEmail(email) !== normalizeEmail(info.value.bound_email)) {
      return '链接中的邮箱与卡密绑定邮箱不匹配'
    }
    emailInput.value = ''
    return ''
  }
  if (!email) {
    return '此卡密未绑定邮箱，请在链接中指定邮箱账号'
  }
  if (!email.includes('@')) {
    return '请输入有效邮箱地址'
  }
  emailInput.value = email
  return ''
}

function routeParamString(value: unknown) {
  if (Array.isArray(value)) return String(value[0] || '')
  return String(value || '')
}

function normalizeEmail(value: string) {
  return value.trim().toLowerCase()
}

async function copyEmail() {
  if (!targetEmail.value) return
  try {
    await copyToClipboard(targetEmail.value)
    appStore.showSuccess('邮箱已复制')
  } catch {
    appStore.showError('复制邮箱失败')
  }
}

function showFetchToast(response: Awaited<ReturnType<typeof getPublicMailMessages>>) {
  const message = response.message || (currentMessage.value ? '获取邮件成功' : '未获取到符合条件的邮件')
  if (!currentMessage.value) {
    appStore.showInfo(message)
    return
  }
  if (response.charged) {
    appStore.showSuccess(message)
  } else if (response.repeated) {
    appStore.showWarning(message)
  } else {
    appStore.showInfo(message)
  }
}

function toggleTheme() {
  isDark.value = !isDark.value
}

function startCooldown(seconds: number) {
  window.clearInterval(cooldownTimer)
  cooldown.value = Math.max(0, Math.ceil(seconds))
  if (cooldown.value <= 0) return
  cooldownTimer = window.setInterval(() => {
    cooldown.value = Math.max(0, cooldown.value - 1)
    if (cooldown.value <= 0) {
      window.clearInterval(cooldownTimer)
    }
  }, 1000)
}
</script>

<template>
  <main v-if="autoFetchMode" class="public-mail-raw-page">
    <div v-if="loading || fetching" class="public-mail-raw-state">正在收取邮件...</div>
    <pre v-else class="public-mail-raw-text">{{ pageError || fetchError || autoFetchText || emptyMessage }}</pre>
  </main>

  <main v-else class="public-mail-page" :class="{ 'is-dark-mode': isDark }">
    <header class="public-mail-header">
      <div class="public-mail-brand">
        <span class="public-mail-logo">
          <AppLogo :src="siteLogoSrc" :alt="appStore.siteName.value" />
        </span>
        <span class="public-mail-brand-text">
          <strong>{{ appStore.siteName.value }}</strong>
          <span>{{ appStore.siteSubtitle.value }}</span>
        </span>
      </div>
      <button class="public-mail-theme-button" type="button" :title="isDark ? '切换亮色模式' : '切换暗色模式'" @click="toggleTheme">
        <Sun v-if="isDark" class="h-4 w-4" />
        <Moon v-else class="h-4 w-4" />
      </button>
    </header>

    <section v-if="!loading && pageError" class="public-mail-error-stage">
      <div class="public-mail-error-card">
        <span class="public-mail-error-icon">
          <XCircle class="h-16 w-16" />
        </span>
        <h1>{{ errorTitle }}</h1>
        <p>{{ errorDescription }}</p>
      </div>
    </section>

    <section v-else class="public-mail-shell">
      <section v-if="loading" class="public-mail-loading-stage" aria-label="加载中">
        <Loader2 class="h-6 w-6 animate-spin" />
      </section>

      <template v-else-if="info">
        <div class="public-mail-count-row">
          <span class="public-mail-remaining">{{ remainingText }}</span>
        </div>

        <section class="public-mail-panel">
          <div class="public-mail-form" :class="{ 'is-bound': info.has_bound_email }">
            <div v-if="info.has_bound_email" class="public-mail-bound-display">
              <span class="public-mail-bound-label">绑定邮箱：</span>
              <strong class="public-mail-bound-chip">{{ info.bound_email }}</strong>
            </div>
            <label v-if="!info.has_bound_email" class="public-mail-field">
              <span class="public-mail-field-label">邮箱账号</span>
              <input v-model.trim="emailInput" class="public-mail-input" type="email" placeholder="请输入邮箱账号" @keyup.enter="fetchMessages" />
            </label>

            <div class="public-mail-action-zone" :class="{ 'has-copy': info.has_bound_email }">
              <div class="public-mail-actions">
                <button v-if="info.has_bound_email" class="public-mail-copy" type="button" @click="copyEmail">
                  <Copy class="h-4 w-4" />
                  <span>复制邮箱</span>
                </button>
                <button class="public-mail-fetch" type="button" :disabled="!canFetch" @click="fetchMessages">
                  <RefreshCw class="h-4 w-4" :class="{ 'animate-spin': fetching }" />
                  <span>{{ info.remaining <= 0 ? '次数已用完' : fetching ? '获取中...' : cooldown > 0 ? `${cooldown}s` : '获取邮件' }}</span>
                </button>
              </div>
            </div>
          </div>
        </section>

        <section class="public-mail-result">
          <article v-if="currentMessage" class="public-mail-content">
            <h1 class="public-mail-subject">{{ currentMessage.subject || '无标题' }}</h1>
            <div class="public-mail-meta">
              <span>发件人：{{ currentMessage.from || '-' }}</span>
              <span>收件人：{{ currentMessage.to || '-' }}</span>
              <span>时间：{{ currentMessage.time || '-' }}</span>
            </div>
            <iframe v-if="mailHtmlSrcdoc" class="public-mail-html" sandbox="" :srcdoc="mailHtmlSrcdoc"></iframe>
            <pre v-else-if="mailBodyText" class="public-mail-text">{{ mailBodyText }}</pre>
            <div v-else class="public-mail-empty">
              <Inbox class="h-5 w-5" />
              <span>这封邮件没有可显示的正文</span>
            </div>
          </article>

          <div v-else class="public-mail-empty">
            <Inbox class="h-5 w-5" />
            <span>{{ emptyMessage }}</span>
          </div>
        </section>
      </template>
    </section>
  </main>
</template>

<style scoped>
.public-mail-page {
  position: relative;
  isolation: isolate;
  display: flex;
  min-height: 100vh;
  flex-direction: column;
  gap: 0.85rem;
  background:
    radial-gradient(circle at 12% 0%, rgb(20 184 166 / 0.2), transparent 24rem),
    radial-gradient(circle at 88% 9%, rgb(45 212 191 / 0.13), transparent 28rem),
    #f3f4f6;
  color: #111827;
  padding: 1rem;
}

.public-mail-raw-page {
  width: 100vw;
  min-height: 100vh;
  margin: 0;
  background: #ffffff;
  color: #111827;
}

.public-mail-raw-html {
  display: block;
  width: 100vw;
  height: 100vh;
  border: 0;
  background: #ffffff;
}

.public-mail-raw-text {
  box-sizing: border-box;
  min-height: 100vh;
  margin: 0;
  padding: 1rem;
  background: #ffffff;
  color: #111827;
  font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  font-size: 14px;
  line-height: 1.65;
  white-space: pre-wrap;
  word-break: break-word;
}

.public-mail-raw-state {
  display: flex;
  min-height: 100vh;
  align-items: center;
  justify-content: center;
  box-sizing: border-box;
  padding: 1rem;
  background: #ffffff;
  color: #334155;
  font-size: 14px;
  font-weight: 700;
  text-align: center;
}

.public-mail-raw-state.is-error {
  color: #dc2626;
}

:global(html.dark) .public-mail-page,
.public-mail-page.is-dark-mode {
  color-scheme: dark;
  background:
    radial-gradient(circle at 12% 0%, rgb(20 184 166 / 0.2), transparent 25rem),
    radial-gradient(circle at 88% 4%, rgb(6 182 212 / 0.12), transparent 29rem),
    #061017;
  color: #e5e7eb;
}

.public-mail-page::before {
  content: '';
  position: absolute;
  inset: 0;
  z-index: -1;
  background: linear-gradient(180deg, rgb(255 255 255 / 0.42), transparent 16rem);
  pointer-events: none;
}

:global(html.dark) .public-mail-page::before,
.public-mail-page.is-dark-mode::before {
  background: linear-gradient(180deg, rgb(20 184 166 / 0.08), transparent 18rem);
}

.public-mail-header {
  display: flex;
  flex-shrink: 0;
  align-items: flex-start;
  justify-content: space-between;
  gap: 1rem;
}

.public-mail-brand {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 0.75rem;
}

.public-mail-logo {
  display: inline-flex;
  width: 2.65rem;
  height: 2.65rem;
  flex: 0 0 2.65rem;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  border-radius: 0.75rem;
  background: rgb(20 184 166 / 0.14);
  box-shadow: 0 8px 18px rgb(15 23 42 / 0.08);
}

:global(html.dark) .public-mail-logo,
.public-mail-page.is-dark-mode .public-mail-logo {
  background: rgb(20 184 166 / 0.16);
  box-shadow: 0 10px 24px rgb(0 0 0 / 0.28);
}

.public-mail-brand-text {
  display: flex;
  min-width: 0;
  flex-direction: column;
  line-height: 1.2;
}

.public-mail-brand-text strong {
  overflow: hidden;
  color: #111827;
  font-size: 1rem;
  font-weight: 900;
  text-overflow: ellipsis;
  white-space: nowrap;
}

:global(html.dark) .public-mail-brand-text strong,
.public-mail-page.is-dark-mode .public-mail-brand-text strong {
  color: #f9fafb;
}

.public-mail-brand-text span {
  overflow: hidden;
  margin-top: 0.22rem;
  color: #64748b;
  font-size: 0.78rem;
  font-weight: 700;
  text-overflow: ellipsis;
  white-space: nowrap;
}

:global(html.dark) .public-mail-brand-text span,
.public-mail-page.is-dark-mode .public-mail-brand-text span {
  color: #94a3b8;
}

.public-mail-theme-button {
  display: inline-flex;
  width: 2.35rem;
  height: 2.35rem;
  flex: 0 0 2.35rem;
  align-items: center;
  justify-content: center;
  border: 1px solid #dbe3ea;
  border-radius: 0.65rem;
  background: #ffffff;
  color: #475569;
  box-shadow: 0 8px 18px rgb(15 23 42 / 0.06);
  transition: border-color 0.15s ease, background-color 0.15s ease, color 0.15s ease;
}

.public-mail-theme-button:hover {
  border-color: #14b8a6;
  color: #0f766e;
}

:global(html.dark) .public-mail-theme-button,
.public-mail-page.is-dark-mode .public-mail-theme-button {
  border-color: rgb(45 212 191 / 0.22);
  background: rgb(10 27 35);
  color: #99f6e4;
  box-shadow: 0 10px 24px rgb(0 0 0 / 0.28);
}

.public-mail-shell {
  display: flex;
  width: min(100%, 72rem);
  min-height: 0;
  flex: 1;
  flex-direction: column;
  gap: 0.85rem;
  margin: 0 auto;
}

.public-mail-panel,
.public-mail-result {
  border: 1px solid #e5e7eb;
  border-radius: 0.85rem;
  background: rgb(255 255 255 / 0.94);
  box-shadow: 0 18px 42px rgb(20 184 166 / 0.12), 0 10px 26px rgb(15 23 42 / 0.06);
}

:global(html.dark) .public-mail-panel,
.public-mail-page.is-dark-mode .public-mail-panel,
:global(html.dark) .public-mail-result,
.public-mail-page.is-dark-mode .public-mail-result {
  border-color: rgb(45 212 191 / 0.14);
  background: rgb(9 23 31 / 0.94);
  box-shadow: 0 16px 36px rgb(0 0 0 / 0.28);
}

.public-mail-panel {
  flex-shrink: 0;
  padding: clamp(0.85rem, 1.8vw, 1rem);
}

.public-mail-loading-stage {
  display: flex;
  min-height: 18rem;
  flex: 1;
  align-items: center;
  justify-content: center;
  color: #0f766e;
}

:global(html.dark) .public-mail-loading-stage,
.public-mail-page.is-dark-mode .public-mail-loading-stage {
  color: #99f6e4;
}

.public-mail-state {
  display: flex;
  min-height: 5rem;
  align-items: center;
  justify-content: center;
  gap: 0.65rem;
  color: #475569;
  font-weight: 900;
}

:global(html.dark) .public-mail-state,
.public-mail-page.is-dark-mode .public-mail-state {
  color: #cbd5e1;
}

.public-mail-state.is-error {
  color: #dc2626;
}

:global(html.dark) .public-mail-state.is-error,
.public-mail-page.is-dark-mode .public-mail-state.is-error {
  color: #fca5a5;
}

.public-mail-error-stage {
  display: flex;
  min-height: 0;
  flex: 1;
  align-items: center;
  justify-content: center;
  padding: 1rem;
}

.public-mail-error-card {
  width: min(100%, 31rem);
  border: 1px solid #e5e7eb;
  border-radius: 0.9rem;
  background: rgb(255 255 255 / 0.96);
  padding: 2.4rem 2rem;
  text-align: center;
  box-shadow: 0 24px 60px rgb(15 23 42 / 0.12), 0 18px 48px rgb(20 184 166 / 0.13);
}

:global(html.dark) .public-mail-error-card,
.public-mail-page.is-dark-mode .public-mail-error-card {
  border-color: rgb(45 212 191 / 0.15);
  background: rgb(9 23 31 / 0.96);
  box-shadow: 0 28px 70px rgb(0 0 0 / 0.34);
}

.public-mail-error-icon {
  display: inline-flex;
  color: #f43f5e;
}

.public-mail-error-card h1 {
  margin: 1.15rem 0 0;
  color: #111827;
  font-size: 1.35rem;
  font-weight: 900;
  letter-spacing: 0;
}

:global(html.dark) .public-mail-error-card h1,
.public-mail-page.is-dark-mode .public-mail-error-card h1 {
  color: #f9fafb;
}

.public-mail-error-card p {
  margin: 0.7rem 0 0;
  color: #64748b;
  font-size: 0.9rem;
  font-weight: 700;
  line-height: 1.6;
}

:global(html.dark) .public-mail-error-card p,
.public-mail-page.is-dark-mode .public-mail-error-card p {
  color: #94a3b8;
}

.public-mail-count-row {
  display: flex;
  justify-content: flex-end;
  padding: 0 clamp(0.85rem, 1.8vw, 1rem);
}

.public-mail-remaining {
  display: inline-flex;
  min-height: 1.95rem;
  align-items: center;
  border: 1px solid #99f6e4;
  border-radius: 999px;
  background: #f0fdfa;
  padding: 0 0.78rem;
  color: #0f766e;
  font-size: 0.82rem;
  font-weight: 900;
  white-space: nowrap;
}

:global(html.dark) .public-mail-remaining,
.public-mail-page.is-dark-mode .public-mail-remaining {
  border-color: rgb(45 212 191 / 0.35);
  background: rgb(20 184 166 / 0.12);
  color: #99f6e4;
}

.public-mail-form {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 0.9rem;
  align-items: end;
}

.public-mail-form.is-bound {
  min-height: 0;
  align-items: center;
  padding-top: 0;
}

.public-mail-field {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  min-width: 0;
  align-items: center;
  gap: 0.75rem;
}

.public-mail-field-label,
.public-mail-bound-label {
  color: #64748b;
  font-size: 1rem;
  font-weight: 900;
  white-space: nowrap;
}

:global(html.dark) .public-mail-field-label,
:global(html.dark) .public-mail-bound-label,
.public-mail-page.is-dark-mode .public-mail-field-label,
.public-mail-page.is-dark-mode .public-mail-bound-label {
  color: #94a3b8;
}

.public-mail-input {
  width: 100%;
  min-width: 0;
  height: 2.75rem;
  border: 1px solid #cbd5e1;
  border-radius: 0.65rem;
  background: #ffffff;
  padding: 0 0.9rem;
  color: #111827;
  font-size: 0.9rem;
  outline: none;
  transition: border-color 0.15s ease, box-shadow 0.15s ease;
}

.public-mail-input:focus {
  border-color: #14b8a6;
  box-shadow: 0 0 0 3px rgb(20 184 166 / 0.14);
}

:global(html.dark) .public-mail-input,
.public-mail-page.is-dark-mode .public-mail-input {
  border-color: rgb(45 212 191 / 0.18);
  background: rgb(5 18 26);
  color: #e5e7eb;
}

.public-mail-bound-display {
  display: flex;
  min-width: 0;
  min-height: 2.75rem;
  align-items: center;
  gap: 0.75rem;
  padding: 0;
}

.public-mail-bound-chip {
  display: inline-flex;
  max-width: 100%;
  min-height: 2.35rem;
  align-items: center;
  overflow: hidden;
  border: 1px solid rgb(20 184 166 / 0.42);
  border-radius: 0.5rem;
  background: rgb(20 184 166 / 0.1);
  padding: 0.28rem 0.9rem;
  color: #0f766e;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 1rem;
  font-weight: 900;
  letter-spacing: 0;
  line-height: 1.2;
  text-overflow: ellipsis;
  white-space: nowrap;
}

:global(html.dark) .public-mail-bound-chip,
.public-mail-page.is-dark-mode .public-mail-bound-chip {
  border-color: rgb(45 212 191 / 0.34);
  background: rgb(45 212 191 / 0.1);
  color: #99f6e4;
}

.public-mail-actions {
  display: flex;
  align-items: flex-end;
  gap: 0.65rem;
}

.public-mail-action-zone {
  display: flex;
  justify-content: flex-end;
}

.public-mail-copy,
.public-mail-fetch {
  display: inline-flex;
  height: 2.75rem;
  align-items: center;
  justify-content: center;
  gap: 0.45rem;
  border-radius: 0.65rem;
  padding: 0 0.95rem;
  color: #ffffff;
  font-size: 0.86rem;
  font-weight: 900;
  white-space: nowrap;
  transition: transform 0.15s ease, opacity 0.15s ease, background-color 0.15s ease;
}

.public-mail-copy {
  background: #0ea5e9;
}

.public-mail-fetch {
  min-width: 7.3rem;
  background: #14b8a6;
}

.public-mail-copy:hover,
.public-mail-fetch:hover:not(:disabled) {
  transform: translateY(-1px);
}

.public-mail-copy:hover {
  background: #0284c7;
}

.public-mail-fetch:hover:not(:disabled) {
  background: #0f766e;
}

.public-mail-fetch:disabled {
  cursor: not-allowed;
  opacity: 0.58;
}

.public-mail-result {
  display: flex;
  min-height: 0;
  flex: 1;
  overflow: hidden;
}

.public-mail-content {
  display: flex;
  width: 100%;
  min-height: 0;
  flex-direction: column;
  padding: 1.05rem 1.15rem 1.15rem;
}

.public-mail-subject {
  margin: 0;
  color: #111827;
  font-size: 1.25rem;
  font-weight: 900;
  line-height: 1.25;
  letter-spacing: 0;
}

:global(html.dark) .public-mail-subject,
.public-mail-page.is-dark-mode .public-mail-subject {
  color: #f9fafb;
}

.public-mail-meta {
  display: flex;
  flex-shrink: 0;
  flex-wrap: wrap;
  gap: 0.55rem 1rem;
  margin: 0.7rem 0 0.9rem;
  color: #64748b;
  font-size: 0.82rem;
  font-weight: 800;
}

:global(html.dark) .public-mail-meta,
.public-mail-page.is-dark-mode .public-mail-meta {
  color: #94a3b8;
}

.public-mail-html,
.public-mail-text {
  flex: 1;
  min-height: 0;
  border: 1px solid #e5e7eb;
  border-radius: 0.65rem;
}

.public-mail-html {
  width: 100%;
  background: #ffffff;
}

:global(html.dark) .public-mail-html,
.public-mail-page.is-dark-mode .public-mail-html,
:global(html.dark) .public-mail-text,
.public-mail-page.is-dark-mode .public-mail-text {
  border-color: rgb(45 212 191 / 0.14);
}

.public-mail-text {
  overflow: auto;
  margin: 0;
  background: #f8fafc;
  padding: 1rem;
  color: #1f2937;
  font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  font-size: 0.9rem;
  line-height: 1.65;
  white-space: pre-wrap;
  word-break: break-word;
}

:global(html.dark) .public-mail-text,
.public-mail-page.is-dark-mode .public-mail-text {
  background: rgb(4 16 24);
  color: #e5e7eb;
}

.public-mail-empty {
  display: flex;
  width: 100%;
  min-height: 0;
  flex: 1;
  align-items: center;
  justify-content: center;
  gap: 0.6rem;
  padding: 1.5rem;
  color: #94a3b8;
  font-size: 0.88rem;
  font-weight: 900;
  text-align: center;
}

:global(html.dark) .public-mail-empty,
.public-mail-page.is-dark-mode .public-mail-empty {
  color: #64748b;
}

@media (max-width: 720px) {
  .public-mail-page {
    gap: 0.75rem;
    padding: 0.75rem;
  }

  .public-mail-brand-text strong,
  .public-mail-brand-text span {
    max-width: calc(100vw - 7.5rem);
  }

  .public-mail-form {
    grid-template-columns: minmax(0, 1fr);
  }

  .public-mail-form.is-bound {
    min-height: 0;
  }

  .public-mail-action-zone {
    display: block;
  }

  .public-mail-actions {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    width: 100%;
  }

  .public-mail-copy,
  .public-mail-fetch {
    width: 100%;
  }

  .public-mail-content {
    padding: 0.9rem;
  }
}
</style>
