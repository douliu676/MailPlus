<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { Eye, EyeOff, ImageUp, KeyRound, Save, ShieldCheck, Trash2 } from 'lucide-vue-next'
import { useAppStore } from '../stores/app'
import { clearAuthSession, getSessionItem, setSessionItem } from '../api/session'
import { getQuickMailKeyStatus, updateQuickMailKey } from '../api/quickMail'

type ProfileUser = {
  id?: number
  username: string
  email: string
  role: string
  balance?: number
  status?: string
  created_at?: string
  avatar_url?: string
}

const appStore = useAppStore()
const router = useRouter()
const avatarDraft = ref('')
const profileSaving = ref(false)
const passwordSaving = ref(false)
const avatarSaving = ref(false)
const quickMailKeySaving = ref(false)
const quickMailKeyConfigured = ref(false)
const quickMailKeyVisible = ref(false)
const oldPasswordVisible = ref(false)
const newPasswordVisible = ref(false)
const confirmPasswordVisible = ref(false)

const defaultUser: ProfileUser = {
  username: 'admin',
  email: 'admin@example.com',
  role: 'admin',
  balance: 0,
  status: 'active',
}

function readStoredUser(): ProfileUser {
  const raw = getSessionItem('auth_user')
  if (!raw) return defaultUser

  try {
    return { ...defaultUser, ...JSON.parse(raw) }
  } catch {
    return defaultUser
  }
}

const user = ref<ProfileUser>(readStoredUser())
const profileForm = reactive({
  username: user.value.username,
})
const passwordForm = reactive({
  old_password: '',
  new_password: '',
  confirm_password: '',
})
const quickMailKeyForm = reactive({
  key: '',
})
const displayName = computed(() => user.value.username?.trim() || user.value.email?.split('@')[0] || 'admin')
const avatarInitial = computed(() => displayName.value.charAt(0).toUpperCase() || 'A')
const avatarPreviewUrl = computed(() => avatarDraft.value || user.value.avatar_url || '')
const roleLabel = computed(() => (user.value.role === 'admin' ? '管理员' : '用户'))
const statusLabel = computed(() => (user.value.status === 'disabled' ? '禁用' : '启用'))
const formattedBalance = computed(() => {
  const value = Number(user.value.balance || 0)
  return `$${Number.isFinite(value) ? value.toFixed(2) : '0.00'}`
})
const memberSince = computed(() => {
  if (!user.value.created_at) return '-'
  const date = new Date(user.value.created_at)
  if (Number.isNaN(date.getTime())) return '-'
  return new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: 'short' }).format(date)
})

function persistUser(nextUser: ProfileUser) {
  user.value = { ...user.value, ...nextUser }
  profileForm.username = user.value.username
  setSessionItem('auth_user', JSON.stringify(user.value))
  window.dispatchEvent(new Event('storage'))
}

function currentUserID() {
  return user.value.id ? String(user.value.id) : '1'
}

async function logoutAfterCredentialChange(message: string) {
  appStore.showSuccess(message)
  await new Promise((resolve) => window.setTimeout(resolve, 3000))
  clearAuthSession()
  window.dispatchEvent(new Event('storage'))
  await router.replace('/login')
}

async function apiRequest<T>(url: string, options: RequestInit): Promise<T> {
  const response = await fetch(url, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      'X-User-ID': currentUserID(),
      ...(options.headers || {}),
    },
  })
  const result = await response.json().catch(() => ({ code: 500, msg: '请求失败' }))
  if (!response.ok || result.code !== 0) {
    throw new Error(result.msg || '请求失败')
  }
  return result.data as T
}

async function loadProfile() {
  try {
    const latest = await apiRequest<ProfileUser>('/api/user/profile', {
      method: 'GET',
    })
    persistUser(latest)
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '获取个人资料失败')
  }
}

async function loadQuickMailKeyStatus() {
  try {
    const status = await getQuickMailKeyStatus()
    quickMailKeyConfigured.value = Boolean(status.configured)
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '读取快速取件秘钥失败')
  }
}

async function updateProfile() {
  if (!profileForm.username.trim()) {
    appStore.showError('用户名不能为空')
    return
  }

  profileSaving.value = true
  try {
    const updated = await apiRequest<ProfileUser>('/api/user/profile', {
      method: 'PUT',
      body: JSON.stringify({
        username: profileForm.username.trim(),
      }),
    })
    persistUser(updated)
    await logoutAfterCredentialChange('资料更新成功，请重新登录')
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '资料更新失败')
  } finally {
    profileSaving.value = false
  }
}

async function changePassword() {
  if (passwordForm.new_password !== passwordForm.confirm_password) {
    appStore.showError('两次输入的密码不一致')
    return
  }
  if (passwordForm.new_password.length < 8) {
    appStore.showError('密码至少需要 8 个字符')
    return
  }

  passwordSaving.value = true
  try {
    await apiRequest('/api/user/password', {
      method: 'PUT',
      body: JSON.stringify({
        old_password: passwordForm.old_password,
        new_password: passwordForm.new_password,
      }),
    })
    passwordForm.old_password = ''
    passwordForm.new_password = ''
    passwordForm.confirm_password = ''
    oldPasswordVisible.value = false
    newPasswordVisible.value = false
    confirmPasswordVisible.value = false
    await logoutAfterCredentialChange('密码修改成功，请重新登录')
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '密码修改失败')
  } finally {
    passwordSaving.value = false
  }
}

function readFileAsDataURL(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(typeof reader.result === 'string' ? reader.result : '')
    reader.onerror = () => reject(reader.error ?? new Error('读取所选图片失败'))
    reader.readAsDataURL(file)
  })
}

async function handleAvatarFileChange(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  if (!file.type.startsWith('image/')) {
    appStore.showError('请选择图片文件')
    return
  }

  try {
    avatarDraft.value = await readFileAsDataURL(file)
  } catch {
    appStore.showError('读取所选图片失败')
  }
}

async function saveAvatar() {
  if (!avatarDraft.value) {
    appStore.showError('请先上传头像图片')
    return
  }

  avatarSaving.value = true
  try {
    const updated = await apiRequest<ProfileUser>('/api/user/profile', {
      method: 'PUT',
      body: JSON.stringify({
        username: user.value.username,
        avatar_url: avatarDraft.value,
      }),
    })
    persistUser(updated)
    avatarDraft.value = ''
    appStore.showSuccess('头像已更新')
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '头像更新失败')
  } finally {
    avatarSaving.value = false
  }
}

async function deleteAvatar() {
  if (!user.value.avatar_url && !avatarDraft.value) {
    appStore.showError('当前没有可删除的头像')
    return
  }

  avatarSaving.value = true
  try {
    const updated = await apiRequest<ProfileUser>('/api/user/profile', {
      method: 'PUT',
      body: JSON.stringify({
        username: user.value.username,
        avatar_url: '',
      }),
    })
    persistUser(updated)
    avatarDraft.value = ''
    appStore.showSuccess('头像已删除')
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '头像删除失败')
  } finally {
    avatarSaving.value = false
  }
}

async function saveQuickMailKey() {
  const key = quickMailKeyForm.key.trim()
  if (!key) {
    appStore.showError('请输入秘钥')
    return
  }

  quickMailKeySaving.value = true
  try {
    const status = await updateQuickMailKey(key)
    quickMailKeyConfigured.value = Boolean(status.configured)
    quickMailKeyForm.key = ''
    quickMailKeyVisible.value = false
    appStore.showSuccess('快速取件秘钥已保存')
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '保存快速取件秘钥失败')
  } finally {
    quickMailKeySaving.value = false
  }
}

async function clearQuickMailKey() {
  quickMailKeySaving.value = true
  try {
    const status = await updateQuickMailKey('')
    quickMailKeyConfigured.value = Boolean(status.configured)
    quickMailKeyForm.key = ''
    quickMailKeyVisible.value = false
    appStore.showSuccess('快速取件秘钥已清除')
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '清除快速取件秘钥失败')
  } finally {
    quickMailKeySaving.value = false
  }
}

onMounted(() => {
  void loadProfile()
  void loadQuickMailKeyStatus()
})
</script>

<template>
  <div class="mx-auto max-w-[950px] space-y-6">
    <section data-testid="profile-overview-hero" class="card overflow-hidden border border-primary-100/80 bg-gradient-to-br from-primary-50 via-white to-amber-50/70 dark:border-primary-900/40 dark:from-primary-950/40 dark:via-dark-900 dark:to-dark-950">
      <div class="px-6 py-6 md:px-8">
        <div class="flex flex-col gap-6 lg:flex-row lg:items-start">
          <div class="flex h-20 w-20 shrink-0 items-center justify-center overflow-hidden rounded-[1.75rem] bg-gradient-to-br from-primary-500 to-primary-600 text-2xl font-bold text-white shadow-lg shadow-primary-500/20">
            <img v-if="avatarPreviewUrl" :src="avatarPreviewUrl" :alt="displayName" class="h-full w-full object-cover" />
            <span v-else>{{ avatarInitial }}</span>
          </div>

          <div class="min-w-0 flex-1 space-y-5">
            <div class="space-y-3">
              <div class="flex flex-wrap items-center gap-2">
                <h2 class="truncate text-2xl font-semibold text-gray-900 dark:text-white">{{ displayName }}</h2>
                <span class="badge badge-primary">{{ roleLabel }}</span>
                <span class="badge badge-success">{{ statusLabel }}</span>
              </div>
              <p class="truncate text-sm text-gray-600 dark:text-gray-300">{{ user.email || '未绑定邮箱' }}</p>
            </div>

            <div class="grid gap-3 sm:max-w-[520px] sm:grid-cols-2">
              <div class="rounded-2xl bg-white/85 px-4 py-3 shadow-sm ring-1 ring-white/70 dark:bg-dark-900/60 dark:ring-dark-700">
                <p class="text-xs font-medium uppercase tracking-[0.16em] text-gray-400 dark:text-gray-500">账户余额</p>
                <p class="mt-1 text-lg font-semibold leading-6 text-gray-900 dark:text-white">{{ formattedBalance }}</p>
              </div>
              <div class="rounded-2xl bg-white/85 px-4 py-3 shadow-sm ring-1 ring-white/70 dark:bg-dark-900/60 dark:ring-dark-700">
                <p class="text-xs font-medium uppercase tracking-[0.16em] text-gray-400 dark:text-gray-500">注册时间</p>
                <p class="mt-1 text-lg font-semibold leading-6 text-gray-900 dark:text-white">{{ memberSince }}</p>
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>

    <section data-testid="profile-basics-panel" class="card border border-gray-100 bg-white/90 p-6 dark:border-dark-700 dark:bg-dark-900/50">
      <div class="mb-5 flex items-start justify-between gap-4">
        <div>
          <h3 class="text-lg font-semibold text-gray-900 dark:text-white">资料与头像</h3>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">维护公开展示信息，并保持头像与昵称风格一致。</p>
        </div>
      </div>

      <div class="grid gap-6 md:grid-cols-2">
        <div class="rounded-3xl border border-gray-100 bg-gray-50/80 p-5 dark:border-dark-700 dark:bg-dark-900/30">
          <div class="space-y-4">
            <div class="flex h-16 w-16 shrink-0 items-center justify-center overflow-hidden rounded-2xl bg-gradient-to-br from-primary-500 to-primary-600 text-xl font-bold text-white shadow-lg shadow-primary-500/20">
              <img v-if="avatarPreviewUrl" :src="avatarPreviewUrl" :alt="displayName" class="h-full w-full object-cover" />
              <span v-else>{{ avatarInitial }}</span>
            </div>

            <div class="space-y-1">
              <p class="text-sm font-semibold text-gray-900 dark:text-white">资料头像</p>
              <p class="text-sm text-gray-500 dark:text-gray-400">上传图片时会自动压缩静态图片到 20KB 以内，GIF 需自行控制在 20KB 以内</p>
            </div>

            <div class="flex flex-wrap items-center gap-3">
              <label class="btn btn-secondary btn-sm cursor-pointer">
                <ImageUp class="h-4 w-4" />
                <input type="file" accept="image/*" class="hidden" @change="handleAvatarFileChange" />
                上传图片
              </label>
              <button class="btn btn-primary btn-sm" type="button" :disabled="avatarSaving || !avatarDraft" @click="saveAvatar">
                <Save class="h-4 w-4" />
                保存
              </button>
              <button class="btn btn-secondary btn-sm" type="button" :disabled="avatarSaving" @click="deleteAvatar">
                <Trash2 class="h-4 w-4" />
                删除
              </button>
            </div>
          </div>
        </div>

        <div class="rounded-3xl border border-gray-100 bg-gray-50/80 p-5 dark:border-dark-700 dark:bg-dark-900/30">
          <form class="space-y-4" @submit.prevent="updateProfile">
            <p class="text-sm font-semibold text-gray-900 dark:text-white">编辑个人资料</p>
            <div>
              <label class="input-label" for="profile_username">用户名</label>
              <input id="profile_username" v-model="profileForm.username" class="input" type="text" autocomplete="username" placeholder="输入用户名" />
            </div>
            <div class="flex justify-end pt-4">
              <button class="btn btn-primary" type="submit" :disabled="profileSaving">
                <Save class="h-4 w-4" />
                {{ profileSaving ? '更新中...' : '更新资料' }}
              </button>
            </div>
          </form>
        </div>
      </div>
    </section>

    <section class="card">
      <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
        <div class="flex items-center justify-between gap-4">
          <h2 class="text-lg font-medium text-gray-900 dark:text-white">快速取件秘钥</h2>
          <span class="badge" :class="quickMailKeyConfigured ? 'badge-success' : 'badge-danger'">
            {{ quickMailKeyConfigured ? '已设置' : '未设置' }}
          </span>
        </div>
      </div>

      <form class="space-y-4 px-6 py-6" @submit.prevent="saveQuickMailKey">
        <div>
          <label class="input-label" for="quick_mail_key">秘钥</label>
          <div class="relative">
            <input
              id="quick_mail_key"
              v-model.trim="quickMailKeyForm.key"
              class="input pr-12"
              :type="quickMailKeyVisible ? 'text' : 'password'"
              autocomplete="new-password"
              :placeholder="quickMailKeyConfigured ? '输入新秘钥覆盖当前秘钥' : '输入快速取件秘钥'"
            />
            <button
              class="absolute inset-y-0 right-0 inline-flex w-11 items-center justify-center text-gray-400 transition-colors hover:text-primary-500 dark:text-dark-300 dark:hover:text-primary-300"
              type="button"
              :title="quickMailKeyVisible ? '隐藏秘钥' : '显示秘钥'"
              :aria-label="quickMailKeyVisible ? '隐藏秘钥' : '显示秘钥'"
              @click="quickMailKeyVisible = !quickMailKeyVisible"
            >
              <EyeOff v-if="quickMailKeyVisible" class="h-4 w-4" />
              <Eye v-else class="h-4 w-4" />
            </button>
          </div>
        </div>
        <div class="flex flex-wrap justify-end gap-3 pt-4">
          <button class="btn btn-secondary" type="button" :disabled="quickMailKeySaving || !quickMailKeyConfigured" @click="clearQuickMailKey">
            <Trash2 class="h-4 w-4" />
            清除
          </button>
          <button class="btn btn-primary" type="submit" :disabled="quickMailKeySaving">
            <KeyRound class="h-4 w-4" />
            {{ quickMailKeySaving ? '保存中...' : '保存秘钥' }}
          </button>
        </div>
      </form>
    </section>

    <section class="card">
      <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
        <h2 class="text-lg font-medium text-gray-900 dark:text-white">修改密码</h2>
      </div>

      <form class="space-y-4 px-6 py-6" @submit.prevent="changePassword">
        <div>
          <label class="input-label" for="old_password">当前密码</label>
          <div class="relative">
            <input
              id="old_password"
              v-model="passwordForm.old_password"
              class="input pr-12"
              :type="oldPasswordVisible ? 'text' : 'password'"
              required
              autocomplete="current-password"
            />
            <button
              class="absolute inset-y-0 right-0 inline-flex w-11 items-center justify-center text-gray-400 transition-colors hover:text-primary-500 dark:text-dark-300 dark:hover:text-primary-300"
              type="button"
              :title="oldPasswordVisible ? '隐藏当前密码' : '显示当前密码'"
              :aria-label="oldPasswordVisible ? '隐藏当前密码' : '显示当前密码'"
              @click="oldPasswordVisible = !oldPasswordVisible"
            >
              <EyeOff v-if="oldPasswordVisible" class="h-4 w-4" />
              <Eye v-else class="h-4 w-4" />
            </button>
          </div>
        </div>
        <div>
          <label class="input-label" for="new_password">新密码</label>
          <div class="relative">
            <input
              id="new_password"
              v-model="passwordForm.new_password"
              class="input pr-12"
              :type="newPasswordVisible ? 'text' : 'password'"
              required
              autocomplete="new-password"
            />
            <button
              class="absolute inset-y-0 right-0 inline-flex w-11 items-center justify-center text-gray-400 transition-colors hover:text-primary-500 dark:text-dark-300 dark:hover:text-primary-300"
              type="button"
              :title="newPasswordVisible ? '隐藏新密码' : '显示新密码'"
              :aria-label="newPasswordVisible ? '隐藏新密码' : '显示新密码'"
              @click="newPasswordVisible = !newPasswordVisible"
            >
              <EyeOff v-if="newPasswordVisible" class="h-4 w-4" />
              <Eye v-else class="h-4 w-4" />
            </button>
          </div>
          <p class="hint">密码至少需要 8 个字符</p>
        </div>
        <div>
          <label class="input-label" for="confirm_password">确认新密码</label>
          <div class="relative">
            <input
              id="confirm_password"
              v-model="passwordForm.confirm_password"
              class="input pr-12"
              :type="confirmPasswordVisible ? 'text' : 'password'"
              required
              autocomplete="new-password"
            />
            <button
              class="absolute inset-y-0 right-0 inline-flex w-11 items-center justify-center text-gray-400 transition-colors hover:text-primary-500 dark:text-dark-300 dark:hover:text-primary-300"
              type="button"
              :title="confirmPasswordVisible ? '隐藏确认新密码' : '显示确认新密码'"
              :aria-label="confirmPasswordVisible ? '隐藏确认新密码' : '显示确认新密码'"
              @click="confirmPasswordVisible = !confirmPasswordVisible"
            >
              <EyeOff v-if="confirmPasswordVisible" class="h-4 w-4" />
              <Eye v-else class="h-4 w-4" />
            </button>
          </div>
        </div>
        <div class="flex justify-end pt-4">
          <button class="btn btn-primary" type="submit" :disabled="passwordSaving">
            <ShieldCheck class="h-4 w-4" />
            {{ passwordSaving ? '修改中...' : '修改密码' }}
          </button>
        </div>
      </form>
    </section>

  </div>
</template>

<style scoped>
@media (max-width: 640px) {
  .card {
    border-radius: 0.875rem;
  }

  .card.p-6,
  [data-testid="profile-overview-hero"] > div {
    padding: 1rem;
  }

  .truncate {
    white-space: normal;
    overflow-wrap: anywhere;
  }

  .btn {
    flex: 1 1 auto;
    min-width: 0;
  }

  form .flex.justify-end {
    align-items: stretch;
    flex-direction: column;
  }

  form .flex.justify-end .btn {
    width: 100%;
  }
}
</style>
