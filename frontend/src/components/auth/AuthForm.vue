<script setup lang="ts">
/**
 * 登录与注册共用的那张表单。
 *
 * 共用的是**形态**（两个字段、密码可见性、提交态、表单内报错），不是页面：
 * `设计说明书.md` §4.1 明写"两个独立页面，不是同一个页面的标签切换"，说的是
 * 路由与落点，不是不许共用组件。差异（标题、按钮字、失败怎么映射、脚注去哪
 * 儿）都从这里传进来。
 *
 * 三件事照着文档做：
 *
 * - **报错在表单内，不跳页**（§4.1）。所以错误是这张表单的一块内容，不是一
 *   个 toast 或一个跳转。
 * - **用户名区分大小写**，所以 `autocapitalize="none"`、`spellcheck="false"`，
 *   且这里**不做 trim、不做小写化** —— 用户在框中敲的是什么，发出去的就是什
 *   么。
 * - **没有"忘记密码"**（§7.2 明列不画），所以这里没有那一条。
 */

import { computed, ref } from 'vue'

const props = defineProps<{
  title: string
  submitLabel: string
  /**
   * 只用来决定密码框的 `autocomplete` —— 浏览器靠它分辨"填一个已有的"和"存
   * 一个新的"。**不拿按钮文案去推**：文案是会被改的，改的那天这个判断会静默
   * 变错。
   */
  mode: 'login' | 'register'
  pending: boolean
  /** 已经翻成人话的那一句。由使用方映射，这一层不认识后端的错误码。 */
  error: string | null
}>()

const emit = defineEmits<{ (event: 'submit', credentials: { username: string; password: string }): void }>()

const username = ref('')
const password = ref('')
const revealed = ref(false)

// 两个都填了才让点。**这不是校验** —— 密码短了、用户名不合规都由服务端说了
// 算，这一条只是别让空表单发出去。
const ready = computed(() => username.value.length > 0 && password.value.length > 0)

function submit(): void {
  if (!ready.value || props.pending) {
    return
  }
  emit('submit', { username: username.value, password: password.value })
}
</script>

<template>
  <div class="auth">
    <form class="auth__card" novalidate @submit.prevent="submit">
      <h1 class="auth__title">{{ props.title }}</h1>

      <label class="auth__field">
        <span class="auth__label">用户名</span>
        <input
          v-model="username"
          class="auth__input"
          type="text"
          name="username"
          autocomplete="username"
          autocapitalize="none"
          autocorrect="off"
          spellcheck="false"
          :disabled="props.pending"
        />
      </label>

      <label class="auth__field">
        <span class="auth__label">密码</span>
        <span class="auth__password">
          <input
            v-model="password"
            class="auth__input"
            :type="revealed ? 'text' : 'password'"
            name="password"
            :autocomplete="props.mode === 'register' ? 'new-password' : 'current-password'"
            :disabled="props.pending"
          />
          <button
            type="button"
            class="auth__reveal"
            :aria-pressed="revealed"
            :aria-label="revealed ? '隐藏密码' : '显示密码'"
            @click="revealed = !revealed"
          >
            {{ revealed ? '隐藏' : '显示' }}
          </button>
        </span>
      </label>

      <!--
        §6.2 硬规则 2：每个错误都要有出口。这里给的出口就是表单本身 —— 用户改
        完输入再点一次。所以提示放在按钮上方，紧挨着他要改的地方。
        `role="alert"` 让读屏软件在它出现时读出来（此时焦点还停在按钮上）。
      -->
      <p v-if="props.error" class="auth__error" role="alert">{{ props.error }}</p>

      <button type="submit" class="auth__submit" :disabled="props.pending || !ready">
        {{ props.pending ? '请稍候…' : props.submitLabel }}
      </button>

      <div class="auth__foot">
        <slot name="footer" />
      </div>
    </form>
  </div>
</template>

<style scoped>
/* 认证页是骨架的例外（§2.1）：不进四段式，是一张居中的卡片。 */
.auth {
  display: grid;
  place-items: center;
  height: 100vh;
  background: var(--surface-sunken);
}

.auth__card {
  display: flex;
  flex-direction: column;
  gap: 14px;
  width: 340px;
  padding: 28px 24px 20px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
  box-shadow: 0 1px 2px rgb(15 23 42 / 5%);
}

.auth__title {
  font-size: 19px;
  font-weight: 650;
}

.auth__field {
  display: flex;
  flex-direction: column;
  gap: 5px;
}

.auth__label {
  color: var(--text-muted);
  font-size: 13px;
}

.auth__input {
  width: 100%;
  padding: 8px 10px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
}

.auth__input:disabled {
  background: var(--surface-sunken);
  color: var(--text-muted);
}

.auth__password {
  position: relative;
  display: block;
}

.auth__password .auth__input {
  padding-right: 52px;
}

.auth__reveal {
  position: absolute;
  top: 50%;
  right: 6px;
  padding: 3px 6px;
  border: 0;
  border-radius: var(--radius-sm);
  background: none;
  color: var(--text-muted);
  font-size: 12px;
  transform: translateY(-50%);
  cursor: pointer;
}

.auth__reveal:hover {
  color: var(--text);
}

/* 失败与加载中不能长得像（§6.2 硬规则 1）：错误有底色和边框，提交中只是把
   按钮文字换掉且置灰。 */
.auth__error {
  padding: 8px 10px;
  border: 1px solid #f3c9c3;
  border-radius: var(--radius-sm);
  background: var(--danger-soft);
  color: var(--danger);
  font-size: 13px;
}

.auth__submit {
  padding: 9px;
  border: 1px solid transparent;
  border-radius: var(--radius-sm);
  background: var(--accent);
  color: #fff;
  font-weight: 600;
  cursor: pointer;
}

.auth__submit:disabled {
  background: #b9c6e6;
  cursor: default;
}

.auth__foot {
  padding-top: 2px;
  text-align: center;
  font-size: 13px;
}
</style>
