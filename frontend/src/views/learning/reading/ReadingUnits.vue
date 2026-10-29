<script setup lang="ts">
/**
 * 正文单元。
 *
 * 单元进入视口附近时由父组件注册的 `bindUnit` 触发懒加载；这里不自己发请求。
 *
 * markdown 材料按块排版，纯文本材料仍然是一整段（只看内容，不做标记猜测）。
 * 两条路都走 `ReadingRuns`：每个文字片段带着原文坐标，高亮、批注与选区共用
 * 同一套切分。
 */

import type { ReadingMark, ReadingUnit } from '../../../api/reading'
import { describeFailure } from '../../../api/errors'
import { READING } from '../../../copy/reading'

import type { MarkdownBlock, MarkdownRun } from './markdown'
import { parseMarkdown } from './markdown'
import ReadingRuns from './ReadingRuns.vue'
import type { HighlightRange } from './text'
import type { UnitEntry } from './types'

const props = defineProps<{
  entries: UnitEntry[]
  marks: ReadingMark[]
  highlight: HighlightRange | null
  bindUnit: (element: unknown, ordinal: number) => void
  /** 这份材料按 markdown 排版。 */
  markdown: boolean
}>()

defineEmits<{
  load: [ordinal: number]
  capture: []
}>()

/** 单元文本只在落定后解析一次；滚动重绘不该重切一遍。 */
const parsed = new Map<string, MarkdownBlock[]>()

function plainRuns(unit: ReadingUnit, text: string): MarkdownRun[] {
  return [{ text, start: unit.char_start, strong: false, em: false, code: false, link: '' }]
}

function blocks(unit: ReadingUnit, text: string): MarkdownBlock[] {
  if (!props.markdown) return [{ kind: 'paragraph', runs: plainRuns(unit, text) }]
  let cached = parsed.get(text)
  if (cached === undefined) {
    cached = parseMarkdown(text)
    parsed.set(text, cached)
  }
  return cached
}

/** 正文里不出现 h1（标题在阅读卡顶上），`#` 落到 h2。 */
function headingTag(level: number): string {
  return `h${Math.min(level + 1, 6)}`
}
</script>

<template>
  <div class="units" @mouseup="$emit('capture')" @keyup="$emit('capture')">
    <article
      v-for="{ unit, state } in entries"
      :key="unit.ordinal"
      class="unit"
      :ref="(el) => bindUnit(el, unit.ordinal)"
    >
      <template v-if="state?.status === 'ready'">
        <div v-if="markdown" class="unit__body" data-unit-body :data-ordinal="unit.ordinal">
          <template v-for="(block, bi) in blocks(unit, state.text)" :key="bi">
            <component
              :is="headingTag(block.level)"
              v-if="block.kind === 'heading'"
              class="md-heading"
              :class="`md-heading--${block.level}`"
            >
              <ReadingRuns :unit="unit" :runs="block.runs" :marks="marks" :highlight="highlight" />
            </component>

            <p v-else-if="block.kind === 'paragraph'" class="md-p">
              <ReadingRuns :unit="unit" :runs="block.runs" :marks="marks" :highlight="highlight" />
            </p>

            <ol v-else-if="block.kind === 'list' && block.ordered" class="md-list">
              <li v-for="(item, i) in block.items" :key="i">
                <ReadingRuns :unit="unit" :runs="item" :marks="marks" :highlight="highlight" />
              </li>
            </ol>

            <ul v-else-if="block.kind === 'list'" class="md-list">
              <li v-for="(item, i) in block.items" :key="i">
                <ReadingRuns :unit="unit" :runs="item" :marks="marks" :highlight="highlight" />
              </li>
            </ul>

            <blockquote v-else-if="block.kind === 'quote'" class="md-quote">
              <ReadingRuns :unit="unit" :runs="block.runs" :marks="marks" :highlight="highlight" />
            </blockquote>

            <pre v-else-if="block.kind === 'code'" class="md-code"><code><ReadingRuns :unit="unit" :runs="block.runs" :marks="marks" :highlight="highlight" /></code></pre>

            <hr v-else class="md-hr" />
          </template>
        </div>

        <p v-else class="unit__text" data-unit-body :data-ordinal="unit.ordinal">
          <ReadingRuns
            :unit="unit"
            :runs="plainRuns(unit, state.text)"
            :marks="marks"
            :highlight="highlight"
          />
        </p>
      </template>

      <p v-else-if="state?.status === 'loading'" class="unit__pending">
        {{ READING.unitLoading }}
      </p>

      <div v-else-if="state?.status === 'failed'" class="unit__failed">
        <p class="unit__failed-text">
          {{ READING.unitFailed }}：{{ state.failure ? describeFailure(state.failure) : '' }}
        </p>
        <button type="button" class="ghost" @click="$emit('load', unit.ordinal)">
          {{ READING.retry }}
        </button>
      </div>

      <div v-else class="unit__lazy">
        <button type="button" class="ghost" @click="$emit('load', unit.ordinal)">
          {{ READING.loadUnit }}
        </button>
      </div>
    </article>
  </div>
</template>

<style scoped src="./controls.css"></style>
<style scoped src="./units.css"></style>
