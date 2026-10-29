/**
 * 单元文本的切段与高亮。
 *
 * 坐标全部是材料内的绝对字符偏移；这里只把它们映射到当前单元的局部下标。
 * 高亮区间与批注区间共用同一套切分，所以一段文字同时被引用与批注覆盖时不会
 * 出现两套互相打架的标记。
 */

import type { ReadingMark, ReadingUnit } from '../../../api/reading'

export interface Piece {
  text: string
  /** 这段文字的第一个字在**单元内**的偏移。 */
  start: number
  markIds: string[]
  deep: boolean
}

export interface PieceRange {
  start: number
  end: number
  markId: string
  deep: boolean
}

export interface HighlightRange {
  ordinal: number
  start: number
  end: number
}

export function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max)
}

/**
 * 这一段里被高亮与批注覆盖的区间，坐标是**单元内**的偏移。
 *
 * 高亮区间与批注区间共用这一份切分，所以一段文字同时被引用与批注覆盖时不会
 * 出现两套互相打架的标记。
 */
export function markRanges(
  unit: ReadingUnit,
  marks: readonly ReadingMark[],
  highlight: HighlightRange | null,
): PieceRange[] {
  const ranges: PieceRange[] = []
  if (highlight && highlight.ordinal === unit.ordinal) {
    const start = clamp(highlight.start - unit.char_start, 0, unit.char_end - unit.char_start)
    const end = clamp(highlight.end - unit.char_start, 0, unit.char_end - unit.char_start)
    if (end > start) ranges.push({ start, end, markId: '', deep: true })
  }
  for (const mark of marks) {
    if (mark.locator.ordinal !== unit.ordinal) continue
    const start = clamp(mark.locator.char_start - unit.char_start, 0, unit.char_end - unit.char_start)
    const end = clamp(mark.locator.char_end - unit.char_start, 0, unit.char_end - unit.char_start)
    if (end <= start) continue
    ranges.push({ start, end, markId: mark.mark_id, deep: false })
  }
  return ranges
}

/**
 * 把一段文字按标记区间切开。`base` 是这段文字第一个字在单元内的偏移 ——
 * markdown 渲染出来的一段文字往往只是原文的一部分，所以不能假定从 0 开始。
 */
export function splitText(
  text: string,
  base: number,
  ranges: readonly PieceRange[],
): Piece[] {
  const end = base + text.length
  const offsets = new Set<number>([base, end])
  for (const range of ranges) {
    if (range.start > base && range.start < end) offsets.add(range.start)
    if (range.end > base && range.end < end) offsets.add(range.end)
  }
  const sorted = [...offsets].sort((a, b) => a - b)
  const pieces: Piece[] = []
  for (let i = 0; i + 1 < sorted.length; i += 1) {
    const start = sorted[i]
    const stop = sorted[i + 1]
    if (start === undefined || stop === undefined || stop <= start) continue
    const active = ranges.filter((range) => range.start <= start && range.end >= stop)
    pieces.push({
      text: text.slice(start - base, stop - base),
      start,
      markIds: active.map((range) => range.markId).filter((id) => id !== ''),
      deep: active.some((range) => range.deep),
    })
  }
  return pieces
}

export function pieceTitle(piece: Piece, marks: readonly ReadingMark[]): string {
  const bodies = piece.markIds
    .map((id) => marks.find((mark) => mark.mark_id === id))
    .filter((mark): mark is ReadingMark => mark !== undefined)
    .map((mark) => mark.body?.trim() ?? '')
    .filter((body) => body !== '')
  return bodies.join('\n')
}
