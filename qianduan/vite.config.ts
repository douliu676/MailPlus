import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { readFileSync } from 'node:fs'

function isValidPort(value: string | undefined) {
  if (!value) {
    return false
  }
  const port = Number(value.trim())
  return Number.isInteger(port) && port > 0 && port <= 65535
}

function readBackendPort() {
  const envPort = process.env.BACKEND_PORT || process.env.PORT
  if (isValidPort(envPort)) {
    return envPort!.trim()
  }

  try {
    const envContent = readFileSync(new URL('../houduan/.env', import.meta.url), 'utf8')
    for (const line of envContent.split(/\r?\n/)) {
      const match = line.trim().match(/^PORT\s*=\s*(.*)$/)
      if (!match) {
        continue
      }
      const port = match[1].trim().replace(/^['"]|['"]$/g, '')
      if (isValidPort(port)) {
        return port
      }
    }
  } catch {
    // The backend .env is optional; local development falls back to 4400.
  }

  return '4400'
}

const backendTarget = process.env.BACKEND_URL?.trim() || `http://localhost:${readBackendPort()}`

export default defineConfig({
  plugins: [vue()],
  server: {
    port: 4399,
    strictPort: true,
    proxy: {
      '^/(imap|outlook)/mail/[^/]+/all/[^/]+(?:/[0-9]+)?(?:\\?.*)?$': {
        target: backendTarget,
        changeOrigin: true,
      },
      '^/mail/[^/]+/all(?:/.*)?(?:\\?.*)?$': {
        target: backendTarget,
        changeOrigin: true,
        rewrite: (path) => path.replace(/^\/mail\//, '/api/public/mail/'),
      },
      '/api': {
        target: backendTarget,
        changeOrigin: true,
      },
    },
  },
  preview: {
    port: 4399,
    strictPort: true,
  },
})
