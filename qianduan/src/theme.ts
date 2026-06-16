import { ref, watch } from 'vue'

const savedTheme = localStorage.getItem('theme')
const isDark = ref(savedTheme ? savedTheme === 'dark' : true)

function applyTheme(value: boolean) {
  document.documentElement.classList.toggle('dark', value)
  localStorage.setItem('theme', value ? 'dark' : 'light')
}

watch(isDark, applyTheme, { immediate: true })

export function useTheme() {
  return { isDark }
}
