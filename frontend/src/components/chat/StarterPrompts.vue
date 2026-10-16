<script setup lang="ts">
/**
 * 对话首页上那几条可点即发的问题（§4.2）。
 *
 * 空态给的是**动作**不是解释（§7.1 第 2 条），所以这里全是按钮，没有一句"你
 * 还没有会话"。点下去就建会话并把这句话当作第一轮发出去 —— 那是使用方的事，
 * 这一层只管挑哪一句。
 */

const emit = defineEmits<{
  (event: 'pick', text: string): void
}>()

const suggestions: readonly string[] = [
  '用三句话说明什么是复利',
  '帮我列一个两周的复习计划',
  '这段话的意思是什么：温故而知新',
]
</script>

<template>
  <div class="starter">
    <button
      v-for="suggestion in suggestions"
      :key="suggestion"
      type="button"
      class="starter__item"
      @click="emit('pick', suggestion)"
    >
      {{ suggestion }}
    </button>
  </div>
</template>

<style scoped>
.starter {
  display: flex;
  flex-direction: column;
  gap: 8px;
  align-items: center;
  padding: 26vh 24px 24px;
}

.starter__item {
  width: 100%;
  max-width: 420px;
  padding: 11px 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
  text-align: left;
  cursor: pointer;
}

.starter__item:hover {
  border-color: var(--accent);
  color: var(--accent);
}
</style>
