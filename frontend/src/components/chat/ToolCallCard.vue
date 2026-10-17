<script setup lang="ts">
/**
 * 一次工具调用。参数与结果都可能很长，所以默认收起，但必须在那儿 ——
 * `设计说明书.md` §3.5 把"AI 查了什么"列为透明度的一部分。
 *
 * 结果按**纯文本**画：它是工具的输出，不是模型写的 Markdown。把工具结果丢进
 * Markdown 渲染器等于让一个外部数据源决定页面结构。
 */

defineProps<{
  name: string
  args?: string
  result?: string
  ok?: boolean
}>()
</script>

<template>
  <details class="tool">
    <summary class="tool__summary">
      <span class="tool__name">{{ name || '工具调用' }}</span>
      <span v-if="ok === true" class="tool__state tool__state--ok">完成</span>
      <span v-else-if="ok === false" class="tool__state tool__state--fail">失败</span>
      <span v-else class="tool__state">进行中</span>
    </summary>
    <div class="tool__body">
      <template v-if="args">
        <p class="tool__key">参数</p>
        <pre class="tool__pre">{{ args }}</pre>
      </template>
      <template v-if="result">
        <p class="tool__key">结果</p>
        <pre class="tool__pre">{{ result }}</pre>
      </template>
      <p v-else class="tool__pending">还没有结果</p>
    </div>
  </details>
</template>

<style scoped>
.tool {
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
  font-size: 13px;
}

.tool__summary {
  display: flex;
  gap: 8px;
  align-items: center;
  padding: 7px 10px;
  cursor: pointer;
}

.tool__name {
  font-family: var(--font-mono);
  font-size: 12px;
}

.tool__state {
  color: var(--text-faint);
  font-size: 11px;
}

.tool__state--ok {
  color: var(--success);
}

.tool__state--fail {
  color: var(--danger);
}

.tool__body {
  padding: 0 10px 9px;
  border-top: 1px solid var(--border);
}

.tool__key {
  margin: 7px 0 3px;
  color: var(--text-faint);
  font-size: 11px;
}

.tool__pre {
  max-height: 240px;
  margin: 0;
  padding: 7px 9px;
  border-radius: 4px;
  background: var(--surface-sunken);
  overflow: auto;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: 12px;
}

.tool__pending {
  margin: 7px 0 0;
  color: var(--text-faint);
}
</style>
