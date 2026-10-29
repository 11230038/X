<script setup lang="ts">
/**
 * 一段文字的叶子：行内样式、高亮、批注与链接。
 *
 * 每个文字片段都带 `data-src`（它在材料原文里的起点）。渲染时丢掉了 `**`
 * 这类语法字符，但选区能靠这个属性换算回原文坐标 —— 见 `useReadingSelection`。
 * 因此这里**不嵌套**元素：一个片段就是一个直接包着文字的 `<span>`/`<mark>`。
 */

import { computed } from 'vue'

import type { ReadingMark, ReadingUnit } from '../../../api/reading'

import type { MarkdownRun } from './markdown'
import { markRanges, pieceTitle, splitText, type HighlightRange, type Piece } from './text'

const props = defineProps<{
  unit: ReadingUnit
  runs: MarkdownRun[]
  marks: ReadingMark[]
  highlight: HighlightRange | null
}>()

const ranges = computed(() => markRanges(props.unit, props.marks, props.highlight))

/** 每一段先按标记区间切开，模板只负责画。 */
const laid = computed(() =>
  props.runs
    .filter((run) => run.text !== '')
    .map((run) => ({
      run,
      pieces: splitText(run.text, run.start - props.unit.char_start, ranges.value),
    })),
)

function source(piece: Piece): number {
  return props.unit.char_start + piece.start
}

function title(piece: Piece): string {
  return pieceTitle(piece, props.marks)
}

function isNoted(piece: Piece): boolean {
  return piece.markIds.some(
    (id) => props.marks.find((mark) => mark.mark_id === id)?.kind === 'note',
  )
}

function styleClass(run: MarkdownRun): Record<string, boolean> {
  return { 'rt-strong': run.strong, 'rt-em': run.em, 'rt-code': run.code }
}
</script>

<template>
  <template v-for="(line, index) in laid" :key="index">
    <template v-for="(piece, i) in line.pieces" :key="i">
      <mark
        v-if="piece.markIds.length > 0 || piece.deep"
        class="hl"
        :class="[styleClass(line.run), { 'hl--deep': piece.deep, 'hl--noted': isNoted(piece) }]"
        :data-src="source(piece)"
        :title="title(piece)"
      >{{ piece.text }}</mark>
      <a
        v-else-if="line.run.link"
        class="rt-link"
        :class="styleClass(line.run)"
        :href="line.run.link"
        target="_blank"
        rel="noopener noreferrer"
        :data-src="source(piece)"
      >{{ piece.text }}</a>
      <span v-else :class="styleClass(line.run)" :data-src="source(piece)">{{ piece.text }}</span>
    </template>
  </template>
</template>

<style scoped src="./rich-text.css"></style>
