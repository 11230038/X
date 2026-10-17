<script setup lang="ts">
/**
 * 正文单元。
 *
 * 单元进入视口附近时由父组件注册的 `bindUnit` 触发懒加载；这里不自己发请求。
 * 高亮、批注与深链引用共用 `piecesOf` 的切段结果。
 */

import type { ReadingMark, ReadingUnit } from '../../../api/reading'
import { describeFailure } from '../../../api/errors'
import { READING } from '../../../copy/reading'
import type { UnitEntry } from './types'
import { piecesOf, pieceTitle, type HighlightRange, type Piece } from './text'

const props = defineProps<{
  entries: UnitEntry[]
  marks: ReadingMark[]
  highlight: HighlightRange | null
  bindUnit: (element: unknown, ordinal: number) => void
}>()

defineEmits<{
  load: [ordinal: number]
  capture: []
}>()

function pieces(unit: ReadingUnit, text: string): Piece[] {
  return piecesOf(unit, text, props.marks, props.highlight)
}

function title(piece: Piece): string {
  return pieceTitle(piece, props.marks)
}

function isNoted(piece: Piece): boolean {
  return piece.markIds.some(
    (id) => props.marks.find((mark) => mark.mark_id === id)?.kind === 'note',
  )
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
      <header class="unit__head">
        <span class="unit__ordinal">{{ READING.unitLabel(unit.ordinal) }}</span>
        <span class="unit__chars">{{ READING.unitChars(unit.char_end - unit.char_start) }}</span>
      </header>

      <p
        v-if="state?.status === 'ready'"
        class="unit__text"
        :data-unit-body="''"
        :data-ordinal="unit.ordinal"
      >
        <span v-for="(piece, i) in pieces(unit, state.text)" :key="i"><mark
          v-if="piece.markIds.length > 0 || piece.deep"
          class="hl"
          :class="{ 'hl--deep': piece.deep, 'hl--noted': isNoted(piece) }"
          :title="title(piece)"
        >{{ piece.text }}</mark><template v-else>{{ piece.text }}</template></span>
      </p>

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
