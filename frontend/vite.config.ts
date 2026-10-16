import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'

// 后端在开发期不开 CORS：`coder/websocket` 在升级前拿 Origin 的 host 跟请求的
// Host 头比，两者相等才放行。所以这台开发服务器做的是**同源**，不是跨域 ——
// 它把 /api 原样转给后端，浏览器眼里整个应用仍然只有一个源。
//
// `changeOrigin` 因此**刻意留成默认的 false**。设成 true 会把 Host 改写成
// 127.0.0.1:8080，于是后端看到 Host 与 Origin 不一致，握手被拒。这不是一个
// 可以顺手打开的开关，是这条链路成立的前提。
//
// `ws: true` 是 WebSocket 升级透传所必需：没有它，`GET /api/v1/ws` 会退化成
// 普通 HTTP 转发，101 永远到不了浏览器。
export default defineConfig({
  plugins: [vue()],
  server: {
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8080',
        ws: true,
      },
    },
  },
})
