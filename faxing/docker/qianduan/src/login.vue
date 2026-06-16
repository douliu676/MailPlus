<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { Eye, EyeOff, Lock, LogIn, Moon, Sun, User } from 'lucide-vue-next'
import { login } from './api/auth'
import AppLogo from './components/AppLogo.vue'
import { useAppStore } from './stores/app'
import { useTheme } from './theme'

const currentYear = computed(() => new Date().getFullYear())
const appStore = useAppStore()
const router = useRouter()
const showPassword = ref(false)
const isLoading = ref(false)
const { isDark } = useTheme()
const pageTitle = computed(() => `Login - ${appStore.siteName.value}`)
const siteLogoSrc = computed(() => appStore.siteLogo.value)

const formData = reactive({
  account: '',
  password: '',
})

onMounted(() => {
  appStore.fetchPublicSettings()
})

watch(pageTitle, (title) => {
  document.title = title
}, { immediate: true })

async function handleLogin() {
  isLoading.value = true

  try {
    await login({
      account: formData.account,
      password: formData.password,
    })
    appStore.showSuccess('\u767b\u5f55\u6210\u529f\uff01\u6b22\u8fce\u56de\u6765\u3002')
    await router.replace('/admin/dashboard')
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : '\u767b\u5f55\u5931\u8d25')
  } finally {
    isLoading.value = false
  }
}
</script>

<template>
  <div class="relative flex min-h-screen flex-col overflow-hidden p-4">
    <div class="absolute inset-0 bg-gradient-to-br from-gray-50 via-primary-50/30 to-gray-100 dark:from-dark-950 dark:via-dark-900 dark:to-dark-950"></div>

    <div class="pointer-events-none absolute inset-0 overflow-hidden">
      <div class="absolute -right-40 -top-40 h-80 w-80 rounded-full bg-primary-400/20 blur-3xl"></div>
      <div class="absolute -bottom-40 -left-40 h-80 w-80 rounded-full bg-primary-500/15 blur-3xl"></div>
      <div class="absolute left-1/2 top-1/2 h-96 w-96 -translate-x-1/2 -translate-y-1/2 rounded-full bg-primary-300/10 blur-3xl"></div>
      <div class="absolute inset-0 bg-[linear-gradient(rgba(20,184,166,0.03)_1px,transparent_1px),linear-gradient(90deg,rgba(20,184,166,0.03)_1px,transparent_1px)] bg-[size:64px_64px]"></div>
    </div>

    <button
      class="absolute right-4 top-4 z-20 inline-flex h-10 w-10 items-center justify-center rounded-xl border border-white/60 bg-white/80 text-gray-600 shadow-glass backdrop-blur transition-colors hover:text-primary-600 dark:border-dark-700 dark:bg-dark-900/80 dark:text-dark-300 dark:hover:text-primary-400"
      type="button"
      :aria-label="isDark ? '\u5207\u6362\u5230\u6d45\u8272\u6a21\u5f0f' : '\u5207\u6362\u5230\u6df1\u8272\u6a21\u5f0f'"
      @click="isDark = !isDark"
    >
      <Sun v-if="isDark" class="h-5 w-5" />
      <Moon v-else class="h-5 w-5" />
    </button>

    <main class="relative z-10 flex flex-1 items-center justify-center">
      <section class="w-full max-w-md">
        <div class="mb-8 text-center">
          <div class="mb-4 inline-flex h-16 w-16 items-center justify-center overflow-hidden rounded-2xl">
            <AppLogo :src="siteLogoSrc" />
          </div>
          <h1 class="mb-2 bg-gradient-to-r from-primary-600 to-primary-500 bg-clip-text text-3xl font-bold text-transparent">
            {{ appStore.siteName.value }}
          </h1>
          <p class="text-sm text-gray-500 dark:text-dark-400">{{ appStore.siteSubtitle.value }}</p>
        </div>

        <div class="rounded-2xl border border-white/60 bg-white/80 p-8 shadow-glass backdrop-blur-xl dark:border-dark-700/70 dark:bg-dark-900/80">
          <div class="space-y-6">
            <div class="text-center">
              <h2 class="text-2xl font-bold text-gray-900 dark:text-white">&#27426;&#36814;&#22238;&#26469;</h2>
              <p class="mt-2 text-sm text-gray-500 dark:text-dark-400">&#30331;&#24405;&#24744;&#30340;&#36134;&#25143;&#20197;&#32487;&#32493;</p>
            </div>

            <form class="space-y-5" @submit.prevent="handleLogin">
              <div>
                <label for="account" class="input-label">&#36134;&#21495;</label>
                <div class="relative">
                  <div class="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3.5">
                    <User class="h-5 w-5 text-gray-400 dark:text-dark-500" />
                  </div>
                  <input
                    id="account"
                    v-model="formData.account"
                    type="text"
                    required
                    autofocus
                    autocomplete="username"
                    :disabled="isLoading"
                    class="input pl-11"
                    placeholder="&#35831;&#36755;&#20837;&#36134;&#21495;"
                  />
                </div>
              </div>

              <div>
                <label for="password" class="input-label">&#23494;&#30721;</label>
                <div class="relative">
                  <div class="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3.5">
                    <Lock class="h-5 w-5 text-gray-400 dark:text-dark-500" />
                  </div>
                  <input
                    id="password"
                    v-model="formData.password"
                    :type="showPassword ? 'text' : 'password'"
                    required
                    autocomplete="current-password"
                    :disabled="isLoading"
                    class="input pl-11 pr-11"
                    placeholder="&#35831;&#36755;&#20837;&#23494;&#30721;"
                  />
                  <button
                    type="button"
                    :disabled="isLoading"
                    class="absolute inset-y-0 right-0 flex items-center pr-3.5 text-gray-400 transition-colors hover:text-gray-600 dark:hover:text-dark-300"
                    @click="showPassword = !showPassword"
                  >
                    <EyeOff v-if="showPassword" class="h-5 w-5" />
                    <Eye v-else class="h-5 w-5" />
                  </button>
                </div>
              </div>

              <button type="submit" :disabled="isLoading" class="btn btn-primary w-full">
                <svg
                  v-if="isLoading"
                  class="-ml-1 mr-2 h-4 w-4 animate-spin text-white"
                  fill="none"
                  viewBox="0 0 24 24"
                >
                  <circle
                    class="opacity-25"
                    cx="12"
                    cy="12"
                    r="10"
                    stroke="currentColor"
                    stroke-width="4"
                  ></circle>
                  <path
                    class="opacity-75"
                    fill="currentColor"
                    d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
                  ></path>
                </svg>
                <LogIn v-else class="mr-2 h-5 w-5" />
                {{ isLoading ? '\u6b63\u5728\u767b\u5f55...' : '\u767b\u5f55' }}
              </button>
            </form>
          </div>
        </div>

      </section>
    </main>

    <footer class="relative z-10 text-center text-xs font-medium text-gray-400 dark:text-dark-500">
      &copy; {{ currentYear }} {{ appStore.siteName.value }}. &#20445;&#30041;&#25152;&#26377;&#26435;&#21033;&#12290;
    </footer>
  </div>
</template>
