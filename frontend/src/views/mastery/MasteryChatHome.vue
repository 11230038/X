<script setup lang="ts">
/**
 * 路径对话的首页。
 *
 * 没有会话时摆在这里：一句说明 + 三条真能点即发的开场。空态给的是**动作**
 * 不是解释（§7.1 第 2 条），所以下面全是按钮，没有了会话也不再多说一句。
 */

import { MASTERY } from '../../copy/mastery'

const emit = defineEmits<{
  (event: 'pick', text: string): void
}>()
</script>

<template>
  <div class="start">
    <p class="start__lead">{{ MASTERY.chatLead }}</p>
    <div class="start__prompts">
      <button
        v-for="prompt in MASTERY.chatPrompts"
        :key="prompt"
        type="button"
        class="start__item"
        @click="emit('pick', prompt)"
      >
        {{ prompt }}
      </button>
    </div>
    <p class="start__hint">{{ MASTERY.chatHint }}</p>
  </div>
</template>

<style scoped>
.start {
  display: flex;
  flex-direction: column;
  gap: 18px;
  align-items: center;
  padding: 22vh 24px 24px;
}

.start__lead {
  max-width: 460px;
  margin: 0;
  color: var(--text-muted);
  font-size: 14px;
  line-height: 1.7;
  text-align: center;
}

.start__prompts {
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 100%;
  max-width: 460px;
}

.start__item {
  width: 100%;
  padding: 13px 16px;
  border: 1px solid var(--glass-border);
  border-radius: 16px;
  background: var(--glass-bg);
  box-shadow: var(--glass-shadow), var(--inset-light);
  text-align: left;
  cursor: pointer;
  transition: transform 0.2s ease, box-shadow 0.2s ease, border-color 0.2s ease;
}

.start__item:hover {
  border-color: var(--accent);
  color: var(--accent);
  transform: translateY(-2px);
  box-shadow: var(--glass-shadow-hover), var(--inset-light);
}

.start__hint {
  max-width: 460px;
  margin: 0;
  color: var(--text-muted);
  font-size: 13px;
  line-height: 1.6;
  text-align: center;
}
</style>
