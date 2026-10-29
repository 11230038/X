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

import AppIcon from '../common/AppIcon.vue'
import logo from '../../assets/logo-rinnex-color.png'

const props = defineProps<{
  title: string
  /** 标题下面的一句定位语。不给就按登录/注册各用一句默认文案。 */
  subtitle?: string
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

const subtitle = computed(
  () => props.subtitle ?? (props.mode === 'login' ? '你的专属一体化学习空间' : '创建你的学习空间'),
)

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
      <img class="auth__logo" :src="logo" alt="" aria-hidden="true" />
      <h1 class="brand-script auth__title">{{ props.title }}</h1>
      <p class="brand-script auth__subtitle">{{ subtitle }}</p>

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
            :title="revealed ? '隐藏密码' : '显示密码'"
            @click="revealed = !revealed"
          >
            <AppIcon :name="revealed ? 'eyeOff' : 'eye'" tone="ink" />
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
/* 认证页是骨架的例外（§2.1）：不进四段式，是一张居中的玻璃卡片。
   底色与全局极光一致，卡片本身按演示稿的登录卡做。 */
.auth {
  display: grid;
  place-items: center;
  height: 100vh;
  background:
    radial-gradient(circle at 10% 10%, rgb(247 169 239 / 30%) 0%, transparent 50%),
    radial-gradient(circle at 90% 90%, rgb(177 140 255 / 20%) 0%, transparent 50%);
}

/* 卡片是**安静的**：鼠标划过时不上浮、不缩放、不改阴影。整张卡片是这一页
   的"纸"，不是可以点的控件 —— 会动的只有里面的输入框、眼睛与按钮。 */
.auth__card {
  display: flex;
  flex-direction: column;
  gap: 20px;
  width: 400px;
  padding: 44px 40px 32px;
  border: 1px solid var(--glass-border);
  border-radius: 32px;
  background: var(--glass-bg);
  box-shadow: 0 24px 64px -12px rgb(51 68 85 / 15%), var(--inset-light);
  text-align: center;
  backdrop-filter: blur(30px);
  -webkit-backdrop-filter: blur(30px);
}

/* 品牌标志：`.product/Rinnex logo 初版方案.pdf` 的手绘稿，只裁掉四周纸白，
   无穷符号、R、星点与圆环都在。原稿 2.34:1，比原来的两倍宽一点，所以高按
   宽度走，不拉成 120×60。 */
.auth__logo {
  width: 160px;
  height: auto;
  margin: 0 auto;
  filter: drop-shadow(0 8px 16px rgb(51 68 85 / 15%));
}

/* 标题与副标题也是花体（全局 `.brand-script` 给字体与渐变）。行楷没有粗体
   字面，用合成粗体会把笔锋糊掉，所以标题按常规字重写。宽度收到内容宽度，
   渐变才正好铺满这一行字。 */
.auth__title {
  width: fit-content;
  margin: 0 auto;
  font-size: 30px;
  font-weight: 400;
  line-height: 1.25;
  letter-spacing: 0.01em;
}

.auth__subtitle {
  width: fit-content;
  margin: -6px auto 8px;
  font-size: 18px;
  font-weight: 400;
}

.auth__field {
  display: flex;
  flex-direction: column;
  gap: 8px;
  text-align: left;
}

.auth__label {
  margin-left: 8px;
  color: var(--text);
  font-size: 14px;
  font-weight: 600;
}

.auth__input {
  width: 100%;
  padding: 14px 20px;
  border: 1px solid rgb(0 0 0 / 6%);
  border-radius: 16px;
  background: rgb(255 255 255 / 70%);
  font-size: 15px;
  outline: none;
  transition: all 0.3s cubic-bezier(0.25, 0.8, 0.25, 1);
}

.auth__input:focus {
  border-color: var(--color-purple);
  background: #fff;
  box-shadow: 0 0 0 4px rgb(177 140 255 / 15%);
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
  padding-right: 48px;
}

/* 显示 / 隐藏密码：一枚手绘眼睛（`AppIcon` 的 `eye` / `eyeOff`）。这里用墨色
   而不是品牌渐变 —— 它在输入框里，颜色要跟着 `color` 变化（悬停变深），和
   输入框本身是一个层级。 */
.auth__reveal {
  position: absolute;
  top: 50%;
  right: 8px;
  display: grid;
  place-items: center;
  width: 32px;
  height: 32px;
  padding: 0;
  border: 0;
  border-radius: 8px;
  background: none;
  color: var(--text-faint);
  --icon-size: 20px;
  transform: translateY(-50%);
  cursor: pointer;
}

.auth__reveal:hover {
  color: var(--text);
}

/* 失败与加载中不能长得像（§6.2 硬规则 1）：错误有底色和边框，提交中只是把
   按钮文字换掉且置灰。 */
.auth__error {
  padding: 10px 14px;
  border: 1px solid #f3c9c3;
  border-radius: var(--radius-sm);
  background: var(--danger-soft);
  color: var(--danger);
  font-size: 14px;
  text-align: left;
}

.auth__submit {
  padding: 16px;
  margin-top: 4px;
  border: 1px solid transparent;
  border-radius: 16px;
  background: var(--accent-gradient);
  color: #fff;
  font-size: 16px;
  font-weight: 700;
  cursor: pointer;
  box-shadow: 0 8px 24px rgb(177 140 255 / 40%);
  transition: all 0.4s cubic-bezier(0.34, 1.56, 0.64, 1);
}

.auth__submit:hover:not(:disabled) {
  transform: translateY(-2px);
  box-shadow: 0 12px 32px rgb(177 140 255 / 50%);
}

.auth__submit:active:not(:disabled) {
  transform: scale(0.98);
}

.auth__submit:disabled {
  background: #d9cdf1;
  box-shadow: none;
  cursor: default;
}

.auth__foot {
  padding-top: 6px;
  color: var(--text-faint);
  text-align: center;
  font-size: 14px;
}

.auth__foot :deep(a) {
  color: var(--accent-ink);
  font-weight: 600;
  text-decoration: none;
}
</style>
