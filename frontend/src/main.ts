import { createApp } from 'vue'

import './style.css'
import App from './App.vue'
import { ensureSession } from './auth/store'
import router from './router'

// Validate any stored token before the first render, so a refreshed page does
// not flash the login screen for a visitor who is already signed in.
async function bootstrap(): Promise<void> {
  await ensureSession()
  createApp(App).use(router).mount('#app')
}

void bootstrap()
