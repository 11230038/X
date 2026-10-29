<script setup lang="ts">
/**
 * 对话首页上那几条可点即发的问题（§4.2）。
 *
 * 版式照演示稿的 `.home`：左上角一个标题、一句说明，下面三张并排的起始提示卡。
 * 每张卡上面写"这次要做什么"，下面是点下去真正发出去的那句话 —— 两行分开，
 * 用户不必从一长句里自己猜这条是用来干什么的。
 *
 * 空态给的是**动作**不是解释（§7.1 第 2 条），所以这里没有一句"你还没有会话"。
 * 点下去就建会话并把那句话当作第一轮发出去 —— 那是使用方的事，这一层只管挑
 * 哪一句。
 */

import { CHAT } from '../../copy/chat'

const emit = defineEmits<{
  (event: 'pick', text: string): void
}>()
</script>

<template>
  <div class="home">
    <h1 class="home__title">{{ CHAT.homeTitle }}</h1>
    <p class="home__hint">{{ CHAT.homeHint }}</p>

    <div class="home__starters">
      <button
        v-for="starter in CHAT.homeStarters"
        :key="starter.title"
        type="button"
        class="starter"
        @click="emit('pick', starter.text)"
      >
        <strong class="starter__title">{{ starter.title }}</strong>
        <span class="starter__text">{{ starter.text }}</span>
      </button>
    </div>
  </div>
</template>

<style scoped>
/* 演示稿 `.home`：居中一列，宽度收在 780px，顶部留白比左右大。 */
.home {
  max-width: 780px;
  margin: 0 auto;
  padding: 48px 24px 24px;
}

.home__title {
  margin: 0 0 6px;
  font-size: 22px;
  font-weight: 650;
}

.home__hint {
  margin: 0 0 24px;
  color: var(--text-muted);
  font-size: 14px;
  line-height: 1.6;
}

/* 演示稿是固定三列：这三条是并排看的，窄屏也不折成一条长列表。 */
.home__starters {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 10px;
}

.starter {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 16px;
  border: 1px solid var(--glass-border);
  border-radius: var(--radius);
  background: var(--glass-bg);
  box-shadow: var(--glass-shadow), var(--inset-light);
  color: var(--text);
  text-align: left;
  cursor: pointer;
  backdrop-filter: blur(20px);
  -webkit-backdrop-filter: blur(20px);
  transition: border-color 0.2s ease, background 0.2s ease, transform 0.2s ease;
}

.starter:hover {
  border-color: rgb(177 140 255 / 45%);
  background: var(--accent-soft);
  transform: translateY(-2px);
}

.starter__title {
  font-size: 15px;
  font-weight: 650;
}

.starter__text {
  color: var(--text-muted);
  font-size: 14px;
  line-height: 1.6;
}
</style>
