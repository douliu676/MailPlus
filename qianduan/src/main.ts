import { createApp } from 'vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import App from './App.vue'
import router from './router'
import { installAuthFetchInterceptor, watchAuthSession } from './api/session'
import { resetTaskSession } from './stores/tasks'
import './style.css'

installAuthFetchInterceptor()

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      gcTime: 10 * 60_000,
      refetchOnWindowFocus: false,
      retry: (failureCount, error) => error?.name !== 'AbortError' && failureCount < 1,
    },
  },
})

resetTaskSession()
watchAuthSession(() => {
  queryClient.clear()
  resetTaskSession()
})

createApp(App).use(VueQueryPlugin, { queryClient }).use(router).mount('#app')
