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
  markIds: string[]
  deep: boolean
}

interface PieceRange {
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

export function piecesOf(
  unit: ReadingUnit,
  text: string,
  marks: readonly ReadingMark[],
  highlight: HighlightRange | null,
): Piece[] {
  const ranges: PieceRange[] = []
  if (highlight && highlight.ordinal === unit.ordinal) {
    const start = clamp(highlight.start - unit.char_start, 0, text.length)
    const end = clamp(highlight.end - unit.char_start, 0, text.length)
    if (end > start) ranges.push({ start, end, markId: '', deep: true })
  }
  for (const mark of marks) {
    if (mark.locator.ordinal !== unit.ordinal) continue
    const start = clamp(mark.locator.char_start - unit.char_start, 0, text.length)
    const end = clamp(mark.locator.char_end - unit.char_start, 0, text.length)
    if (end <= start) continue
    ranges.push({ start, end, markId: mark.mark_id, deep: false })
  }

  const offsets = new Set<number>([0, text.length])
  for (const range of ranges) {
    offsets.add(range.start)
    offsets.add(range.end)
  }
  const sorted = [...offsets].sort((a, b) => a - b)
  const pieces: Piece[] = []
  for (let i = 0; i + 1 < sorted.length; i += 1) {
    const start = sorted[i]
    const end = sorted[i + 1]
    if (start === undefined || end === undefined || end <= start) continue
    const active = ranges.filter((range) => range.start <= start && range.end >= end)
    pieces.push({
      text: text.slice(start, end),
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
