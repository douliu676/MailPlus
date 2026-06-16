import { computed, ref } from 'vue'
import { getPublicSettings, type PublicSettings } from '../api/settings'

declare global {
  interface Window {
    __APP_PUBLIC_SETTINGS__?: PublicSettings
  }
}

export type ToastType = 'success' | 'error' | 'warning' | 'info'
export type ToastSegmentTone = 'normal' | 'success' | 'error'
export type ConfirmTone = 'danger' | 'warning' | 'info'

export type ToastSegment = {
  text: string
  tone?: ToastSegmentTone
}

export type Toast = {
  id: string
  type: ToastType
  message: string
  segments?: ToastSegment[]
  title?: string
  duration?: number
}

export type ConfirmOptions = {
  title?: string
  message: string
  description?: string
  confirmText?: string
  cancelText?: string
  tone?: ConfirmTone
}

export type ConfirmDialogState = Required<Omit<ConfirmOptions, 'description'>> & {
  id: string
  description?: string
}

const publicSettingsLoaded = ref(false)
const publicSettingsLoading = ref(false)
const cachedPublicSettings = ref<PublicSettings | null>(null)
const defaultPublicLogoPath = '/logo.png'
const siteName = ref('\u90ae\u7bb1\u7ba1\u7406\u7cfb\u7edf')
const siteLogo = ref(defaultPublicLogoPath)
const toasts = ref<Toast[]>([])
const confirmDialog = ref<ConfirmDialogState | null>(null)
const publicSettingsStorageKey = 'mail_public_settings'
let toastId = 0
let confirmId = 0
let resolveConfirm: ((value: boolean) => void) | null = null
const defaultToastDuration = 3500

function loadBootstrappedPublicSettings() {
  return window.__APP_PUBLIC_SETTINGS__ || null
}

function loadCachedPublicSettings() {
  try {
    const raw = localStorage.getItem(publicSettingsStorageKey)
    if (!raw) {
      return null
    }
    return JSON.parse(raw) as PublicSettings
  } catch {
    return null
  }
}

const siteSubtitle = computed(
  () => cachedPublicSettings.value?.site_subtitle || '\u6279\u91cf\u8d26\u53f7\u4e0e\u4efb\u52a1\u7ba1\u7406\u5e73\u53f0'
)

function applySettings(settings: PublicSettings, markLoaded = true) {
  const normalizedSettings = {
    ...settings,
    site_logo: settings.site_logo || defaultPublicLogoPath,
  }

  cachedPublicSettings.value = normalizedSettings
  siteName.value = normalizedSettings.site_name || siteName.value
  siteLogo.value = normalizedSettings.site_logo
  publicSettingsLoaded.value = markLoaded
  try {
    localStorage.setItem(publicSettingsStorageKey, JSON.stringify(normalizedSettings))
  } catch {}
}

const bootstrappedPublicSettings = loadBootstrappedPublicSettings()
const cachedInitialPublicSettings = bootstrappedPublicSettings ? null : loadCachedPublicSettings()
if (bootstrappedPublicSettings) {
  applySettings(bootstrappedPublicSettings)
} else if (cachedInitialPublicSettings) {
  applySettings(cachedInitialPublicSettings, false)
}

async function fetchPublicSettings(force = false): Promise<PublicSettings | null> {
  if (publicSettingsLoaded.value && !force) {
    return cachedPublicSettings.value
  }

  if (publicSettingsLoading.value) {
    return null
  }

  publicSettingsLoading.value = true

  try {
    const settings = await getPublicSettings()
    applySettings(settings)
    return settings
  } catch {
    if (!cachedPublicSettings.value) {
      applySettings({})
    } else {
      publicSettingsLoaded.value = true
    }
    return null
  } finally {
    publicSettingsLoading.value = false
  }
}

function showToast(type: ToastType, message: string, title?: string, duration = defaultToastDuration, segments?: ToastSegment[]) {
  const id = `toast-${++toastId}`
  const toast: Toast = { id, type, message, title, duration, segments }
  toasts.value.push(toast)

  if (duration > 0) {
    window.setTimeout(() => hideToast(id), duration)
  }

  return id
}

function hideToast(id: string) {
  const index = toasts.value.findIndex((toast) => toast.id === id)
  if (index >= 0) {
    toasts.value.splice(index, 1)
  }
}

function showSuccess(message: string, title?: string) {
  return showToast('success', message, title)
}

function showError(message: string, title?: string) {
  return showToast('error', message, title)
}

function showWarning(message: string, title?: string) {
  return showToast('warning', message, title)
}

function showInfo(message: string, title?: string) {
  return showToast('info', message, title)
}

function showTaskResult(type: ToastType, segments: ToastSegment[], title?: string) {
  return showToast(type, segments.map((segment) => segment.text).join(''), title, defaultToastDuration, segments)
}

function showConfirm(options: ConfirmOptions): Promise<boolean> {
  if (resolveConfirm) {
    resolveConfirm(false)
  }

  const tone = options.tone || 'danger'
  confirmDialog.value = {
    id: `confirm-${++confirmId}`,
    title: options.title || '确认操作',
    message: options.message,
    description: options.description,
    confirmText: options.confirmText || '确定',
    cancelText: options.cancelText || '取消',
    tone,
  }

  return new Promise((resolve) => {
    resolveConfirm = resolve
  })
}

function closeConfirm(result: boolean) {
  if (resolveConfirm) {
    resolveConfirm(result)
  }
  resolveConfirm = null
  confirmDialog.value = null
}

function confirmAction() {
  closeConfirm(true)
}

function cancelConfirm() {
  closeConfirm(false)
}

export function useAppStore() {
  return {
    publicSettingsLoaded,
    cachedPublicSettings,
    siteName,
    siteLogo,
    siteSubtitle,
    toasts,
    confirmDialog,
    fetchPublicSettings,
    hideToast,
    showSuccess,
    showError,
    showWarning,
    showInfo,
    showTaskResult,
    showConfirm,
    confirmAction,
    cancelConfirm,
  }
}
