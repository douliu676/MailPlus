<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { Eye, EyeOff, Lock, LogOut, ShieldCheck } from 'lucide-vue-next'
import { clearAuthSession, setAuthSessionItem } from '../api/session'
import { useAppStore } from '../stores/app'

const router = useRouter()
const appStore = useAppStore()
const saving = ref(false)
const oldPasswordVisible = ref(false)
const newPasswordVisible = ref(false)
const confirmPasswordVisible = ref(false)

const form = reactive({
  old_password: '',
  new_password: '',
  confirm_password: '',
})

async function submit() {
  if (form.new_password !== form.confirm_password) {
    appStore.showError('两次输入的新密码不一致')
    return
  }
  if (form.new_password.length < 8) {
    appStore.showError('新密码至少需要 8 个字符')
    return
  }
  if (form.new_password === 'admin123') {
    appStore.showError('新密码不能继续使用初始密码')
    return
  }

  saving.value = true
  try {
    const response = await fetch('/api/user/password', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        old_password: form.old_password,
        new_password: form.new_password,
      }),
    })
    const result = await response.json().catch(() => ({ code: 500, msg: '密码修改失败' }))
    if (!response.ok || result.code !== 0) {
      throw new Error(result.msg || '密码修改失败')
    }
    setAuthSessionItem('must_change_password', 'false')
    appStore.showSuccess('密码已修改')
    await router.replace('/admin/dashboard')
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '密码修改失败')
  } finally {
    saving.value = false
  }
}

async function logout() {
  clearAuthSession()
  await router.replace('/login')
}
</script>

<template>
  <main class="relative flex min-h-screen items-center justify-center overflow-hidden bg-gray-50 p-4 text-gray-900 dark:bg-dark-950 dark:text-gray-100">
    <section class="w-full max-w-md rounded-2xl border border-gray-200 bg-white p-6 shadow-xl shadow-gray-900/10 dark:border-dark-800 dark:bg-dark-900">
      <div class="mb-6 flex items-center gap-3">
        <div class="flex h-11 w-11 items-center justify-center rounded-xl bg-primary-100 text-primary-600 dark:bg-primary-500/15 dark:text-primary-300">
          <ShieldCheck class="h-6 w-6" />
        </div>
        <div>
          <h1 class="text-xl font-bold">修改初始密码</h1>
          <p class="text-sm text-gray-500 dark:text-dark-400">首次登录后需要设置新密码</p>
        </div>
      </div>

      <form class="space-y-4" @submit.prevent="submit">
        <label class="block">
          <span class="input-label">当前密码</span>
          <div class="relative">
            <Lock class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" />
            <input v-model="form.old_password" class="input pl-10 pr-11" :type="oldPasswordVisible ? 'text' : 'password'" autocomplete="current-password" />
            <button class="absolute right-2 top-1/2 flex h-8 w-8 -translate-y-1/2 items-center justify-center rounded-lg text-gray-400 hover:text-gray-700 dark:hover:text-gray-200" type="button" @click="oldPasswordVisible = !oldPasswordVisible">
              <EyeOff v-if="oldPasswordVisible" class="h-4 w-4" />
              <Eye v-else class="h-4 w-4" />
            </button>
          </div>
        </label>

        <label class="block">
          <span class="input-label">新密码</span>
          <div class="relative">
            <Lock class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" />
            <input v-model="form.new_password" class="input pl-10 pr-11" :type="newPasswordVisible ? 'text' : 'password'" autocomplete="new-password" />
            <button class="absolute right-2 top-1/2 flex h-8 w-8 -translate-y-1/2 items-center justify-center rounded-lg text-gray-400 hover:text-gray-700 dark:hover:text-gray-200" type="button" @click="newPasswordVisible = !newPasswordVisible">
              <EyeOff v-if="newPasswordVisible" class="h-4 w-4" />
              <Eye v-else class="h-4 w-4" />
            </button>
          </div>
        </label>

        <label class="block">
          <span class="input-label">确认新密码</span>
          <div class="relative">
            <Lock class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" />
            <input v-model="form.confirm_password" class="input pl-10 pr-11" :type="confirmPasswordVisible ? 'text' : 'password'" autocomplete="new-password" />
            <button class="absolute right-2 top-1/2 flex h-8 w-8 -translate-y-1/2 items-center justify-center rounded-lg text-gray-400 hover:text-gray-700 dark:hover:text-gray-200" type="button" @click="confirmPasswordVisible = !confirmPasswordVisible">
              <EyeOff v-if="confirmPasswordVisible" class="h-4 w-4" />
              <Eye v-else class="h-4 w-4" />
            </button>
          </div>
        </label>

        <button class="btn btn-primary w-full" type="submit" :disabled="saving">
          {{ saving ? '保存中...' : '保存并进入后台' }}
        </button>
        <button class="btn btn-secondary w-full" type="button" @click="logout">
          <LogOut class="h-4 w-4" />
          退出登录
        </button>
      </form>
    </section>
  </main>
</template>
