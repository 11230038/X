/**
 * 阅读材料里的 markdown。
 *
 * 为什么不用 markdown-it 直接出 HTML：正文的选区、批注与高亮都按材料里的
 * **原始字符偏移**记账。`##`、`**`、行首的 `-` 一旦被渲染掉，DOM 里的字就和
 * 原文对不上 —— 选区会落到相邻的句子，批注会标错位置。
 *
 * 所以这里自己切块：每个渲染出来的文字片段都带着它在原文里的起点（组件里
 * 落成 `data-src`）。少掉的是语法字符，坐标一个不少；选区仍然能换算回原文。
 *
 * 覆盖阅读材料实际会碰到的那一部分：标题、段落、列表、引用、围栏代码与分隔
 * 线，以及行内的加粗、斜体、代码和链接。表格与嵌套列表不做，碰到时按段落
 * 读 —— 字一个不少，只是少了排版。
 */

export interface RunStyle {
  strong?: boolean
  em?: boolean
  code?: boolean
  link?: string
}

export interface MarkdownRun {
  text: string
  /** 这段文字的第一个字在材料原文里的偏移。 */
  start: number
  strong: boolean
  em: boolean
  code: boolean
  link: string
}

export type MarkdownBlock =
  | { kind: 'heading'; level: number; runs: MarkdownRun[] }
  | { kind: 'paragraph'; runs: MarkdownRun[] }
  | { kind: 'list'; ordered: boolean; items: MarkdownRun[][] }
  | { kind: 'quote'; runs: MarkdownRun[] }
  | { kind: 'code'; runs: MarkdownRun[] }
  | { kind: 'hr' }

interface SourceLine {
  text: string
  /** 这一行第一个字符在原文里的偏移。 */
  start: number
}

interface SourcePart {
  text: string
  start: number
}

/** 行级标记。缩进按最多三个空格算，与 CommonMark 一致。 */
const HEADING = /^ {0,3}(#{1,6})[ \t]+(.*)$/
const FENCE = /^ {0,3}(```|~~~)/
const HR = /^ {0,3}([-*_])(?:[ \t]*\1){2,}[ \t]*$/
const QUOTE = /^ {0,3}>[ \t]?(.*)$/
const BULLET = /^ {0,3}[-*+][ \t]+(.*)$/
const ORDERED = /^ {0,3}(\d{1,9})[.)][ \t]+(.*)$/
/** 段落的终止条件：下一行是另一种块。 */
const BLOCK_START = /^ {0,3}(#{1,6}[ \t]|>|(```|~~~)|([-*+])[ \t]|\d{1,9}[.)][ \t])/

const INLINE_PATTERNS: { re: RegExp; style: (match: RegExpExecArray) => RunStyle }[] = [
  { re: /`([^`\n]+)`/, style: () => ({ code: true }) },
  { re: /\*\*([^*\n]+)\*\*/, style: () => ({ strong: true }) },
  { re: /__([^_\n]+)__/, style: () => ({ strong: true }) },
  { re: /\[([^\]\n]+)\]\(([^)\s]+)\)/, style: (match) => ({ link: match[2] ?? '' }) },
  { re: /\*([^*\n]+)\*/, style: () => ({ em: true }) },
  { re: /_([^_\n]+)_/, style: () => ({ em: true }) },
]

function run(text: string, start: number, style: RunStyle = {}): MarkdownRun {
  return {
    text,
    start,
    strong: style.strong ?? false,
    em: style.em ?? false,
    code: style.code ?? false,
    link: style.link ?? '',
  }
}

function linesOf(source: string): SourceLine[] {
  const lines: SourceLine[] = []
  let start = 0
  for (;;) {
    const newline = source.indexOf('\n', start)
    const end = newline === -1 ? source.length : newline
    lines.push({ text: source.slice(start, end).replace(/\r$/, ''), start })
    if (newline === -1) break
    start = newline + 1
  }
  return lines
}

interface InlineMatch {
  index: number
  end: number
  innerStart: number
  innerEnd: number
  style: RunStyle
}

/** 在一段文字里找最靠前的行内标记；同样靠前时按 `INLINE_PATTERNS` 的顺序。 */
function matchInline(text: string): InlineMatch | null {
  let best: InlineMatch | null = null
  for (const pattern of INLINE_PATTERNS) {
    const match = pattern.re.exec(text)
    if (!match) continue
    const inner = match[1] ?? ''
    if (inner === '') continue
    const offset = match[0].indexOf(inner)
    if (offset === -1) continue
    const candidate: InlineMatch = {
      index: match.index,
      end: match.index + match[0].length,
      innerStart: match.index + offset,
      innerEnd: match.index + offset + inner.length,
      style: pattern.style(match),
    }
    if (best === null || candidate.index < best.index) best = candidate
  }
  return best
}

function parseInline(
  source: string,
  from: number,
  to: number,
  style: RunStyle,
  out: MarkdownRun[],
): void {
  let position = from
  while (position < to) {
    const next = matchInline(source.slice(position, to))
    if (next === null) {
      out.push(run(source.slice(position, to), position, style))
      return
    }
    const start = position + next.index
    if (start > position) out.push(run(source.slice(position, start), position, style))
    parseInline(source, position + next.innerStart, position + next.innerEnd, { ...style, ...next.style }, out)
    position += next.end
  }
}

function inlineRuns(source: string, from: number, to: number): MarkdownRun[] {
  const runs: MarkdownRun[] = []
  parseInline(source, from, to, {}, runs)
  return runs
}

/**
 * 把几行接成一段。行与行之间的软换行在 markdown 里是一个空格；这个空格在
 * 原文里的位置就是上一行末尾的换行符，选区扫过它时不会落到空处。
 */
function runsOfLines(source: string, parts: SourcePart[]): MarkdownRun[] {
  const runs: MarkdownRun[] = []
  parts.forEach((part, index) => {
    if (index > 0) {
      const previous = parts[index - 1]!
      runs.push(run(' ', previous.start + previous.text.length))
    }
    runs.push(...inlineRuns(source, part.start, part.start + part.text.length))
  })
  return runs
}

function partOf(line: SourceLine, content: string): SourcePart {
  const offset = line.text.indexOf(content)
  return { text: content, start: line.start + (offset === -1 ? 0 : offset) }
}

export function parseMarkdown(source: string): MarkdownBlock[] {
  const lines = linesOf(source)
  const blocks: MarkdownBlock[] = []
  let index = 0

  while (index < lines.length) {
    const line = lines[index]!
    if (line.text.trim() === '') {
      index += 1
      continue
    }

    const fence = FENCE.exec(line.text)
    if (fence) {
      const marker = fence[1]!
      const runs: MarkdownRun[] = []
      index += 1
      while (index < lines.length && !lines[index]!.text.trimStart().startsWith(marker)) {
        const code = lines[index]!
        runs.push(run(code.text, code.start))
        index += 1
      }
      index += 1 // 收尾的围栏
      blocks.push({ kind: 'code', runs })
      continue
    }

    const heading = HEADING.exec(line.text)
    if (heading) {
      const content = heading[2]!
      const part = partOf(line, content)
      blocks.push({
        kind: 'heading',
        level: heading[1]!.length,
        runs: inlineRuns(source, part.start, part.start + part.text.length),
      })
      index += 1
      continue
    }

    if (HR.test(line.text)) {
      blocks.push({ kind: 'hr' })
      index += 1
      continue
    }

    if (QUOTE.test(line.text)) {
      const parts: SourcePart[] = []
      while (index < lines.length) {
        const quoted = QUOTE.exec(lines[index]!.text)
        if (!quoted) break
        parts.push(partOf(lines[index]!, quoted[1]!))
        index += 1
      }
      blocks.push({ kind: 'quote', runs: runsOfLines(source, parts) })
      continue
    }

    if (BULLET.test(line.text) || ORDERED.test(line.text)) {
      const ordered = ORDERED.test(line.text)
      const items: MarkdownRun[][] = []
      while (index < lines.length) {
        const marker = ordered ? ORDERED.exec(lines[index]!.text) : BULLET.exec(lines[index]!.text)
        if (!marker) break
        const parts = [partOf(lines[index]!, ordered ? marker[2]! : marker[1]!)]
        index += 1
        // 缩进的续行属于当前这一项。
        while (
          index < lines.length &&
          /^ {2,}\S/.test(lines[index]!.text) &&
          !BLOCK_START.test(lines[index]!.text)
        ) {
          const continuation = lines[index]!
          parts.push(partOf(continuation, continuation.text.trimStart()))
          index += 1
        }
        items.push(runsOfLines(source, parts))
      }
      blocks.push({ kind: 'list', ordered, items })
      continue
    }

    const parts: SourcePart[] = []
    while (index < lines.length) {
      const current = lines[index]!
      if (current.text.trim() === '' || BLOCK_START.test(current.text)) break
      parts.push({ text: current.text, start: current.start })
      index += 1
    }
    blocks.push({ kind: 'paragraph', runs: runsOfLines(source, parts) })
  }

  return blocks
}
