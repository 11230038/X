import vue from '@vitejs/plugin-vue'
import { defineConfig, loadEnv } from 'vite'

// https://vite.dev/config/
export default defineConfig(({ command, mode }) => {
  const env = loadEnv(mode, process.cwd(), 'VITE_')

  // The dev server proxies /api to the Go backend, which keeps the browser on a
  // single origin and removes the need for CORS. Without a target the proxy
  // would silently forward nowhere, so stop instead of degrading.
  const proxy: Record<string, { target: string; changeOrigin: boolean }> = {}
  if (command === 'serve') {
    const target = env.VITE_API_PROXY_TARGET
    if (!target) {
      throw new Error(
        'VITE_API_PROXY_TARGET is required for the dev server; set it in frontend/.env.development',
      )
    }
    proxy['/api'] = { target, changeOrigin: true }
  }

  return {
    plugins: [vue()],
    server: { proxy },
  }
})
