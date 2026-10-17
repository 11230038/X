/**
 * 全应用**唯一**产出 HTML 的地方。
 *
 * 模型输出最终要进 `v-html`，而登录凭据存在 `localStorage` 里 —— 一次 XSS
 * 就等于凭据失窃。所以这里的防线是三层的：
 *
 * 1. `markdown-it` 关掉原生 HTML（`html: false`），输入里的标签只会是文本；
 * 2. 自定义渲染器里所有要拼进字符串的值都过 `escapeHtml`，链接只放行
 *    `http/https/mailto`，图片不加载而是退化成链接；
 * 3. 最后整段再过一次 DOMPurify，即使上面某条规则写漏了，脚本、事件属性、
 *    内联 iframe 也进不来。
 *
 * `trust: false` 给 KaTeX：`\href`、`\htmlClass` 这类宏不能被执行。
 *
 * `MarkdownBody.vue` 是唯一允许使用 `v-html` 的组件，且它只消费本文件的
 * 输出。
 */

import MarkdownIt from 'markdown-it'
import type { StateBlock, StateInline } from 'markdown-it'
import DOMPurify from 'dompurify'
import hljs from 'highlight.js/lib/core'
import bash from 'highlight.js/lib/languages/bash'
import css from 'highlight.js/lib/languages/css'
import go from 'highlight.js/lib/languages/go'
import javascript from 'highlight.js/lib/languages/javascript'
import json from 'highlight.js/lib/languages/json'
import python from 'highlight.js/lib/languages/python'
import sql from 'highlight.js/lib/languages/sql'
import typescript from 'highlight.js/lib/languages/typescript'
import xml from 'highlight.js/lib/languages/xml'
import katex from 'katex'
import 'katex/dist/katex.min.css'
import 'highlight.js/styles/github.css'

hljs.registerLanguage('bash', bash)
hljs.registerLanguage('css', css)
hljs.registerLanguage('go', go)
hljs.registerLanguage('javascript', javascript)
hljs.registerLanguage('js', javascript)
hljs.registerLanguage('json', json)
hljs.registerLanguage('python', python)
hljs.registerLanguage('sql', sql)
hljs.registerLanguage('typescript', typescript)
hljs.registerLanguage('ts', typescript)
hljs.registerLanguage('html', xml)
hljs.registerLanguage('xml', xml)

/** 渲染时用到的上下文：哪些引用编号能对上 `sources` 事件。 */
export interface RenderContext {
  citations?: ReadonlySet<number>
  /** markdown-it 的 `env` 要求可扩展。 */
  [key: string | symbol]: unknown
}

/* ------------------------------------------------------------------ */
/* 数学公式：$$ … $$ 与 $…$。markdown-it 本身不认识，这里补两条规则。       */
/* ------------------------------------------------------------------ */

function isSpace(code: number): boolean {
  return code === 0x20 || code === 0x09
}

function mathInlineRule(state: StateInline, silent: boolean): boolean {
  if (state.src.charCodeAt(state.pos) !== 0x24 /* $ */) return false
  if (state.src.charCodeAt(state.pos + 1) === 0x24) return false
  if (state.pos + 1 >= state.posMax) return false
  // 开头的 $ 后面紧跟空白的不算公式："$ 5" 是钱，不是数学。
  if (isSpace(state.src.charCodeAt(state.pos + 1))) return false

  let end = state.pos + 1
  while (end < state.posMax) {
    const code = state.src.charCodeAt(end)
    if (code === 0x5c /* \ */) {
      end += 2
      continue
    }
    if (code === 0x24) break
    end += 1
  }

  if (end >= state.posMax) return false
  // 结尾的 $ 前面是空白或反斜杠时不算闭合。
  if (isSpace(state.src.charCodeAt(end - 1))) return false
  if (state.src.charCodeAt(end - 1) === 0x5c) return false

  const content = state.src.slice(state.pos + 1, end)
  if (content.length === 0) return false

  if (silent) return true
  const token = state.push('math_inline', 'math', 0)
  token.content = content
  state.pos = end + 1
  return true
}

function mathBlockRule(
  state: StateBlock,
  startLine: number,
  endLine: number,
  silent: boolean,
): boolean {
  const start = state.bMarks[startLine] + state.tShift[startLine]
  const max = state.eMarks[startLine]
  if (start + 2 > max) return false
  if (state.src.charCodeAt(start) !== 0x24 || state.src.charCodeAt(start + 1) !== 0x24) {
    return false
  }

  let content = ''
  let nextLine = startLine + 1

  const firstLine = state.src.slice(start + 2, max)
  const closeOnSameLine = firstLine.indexOf('$$')

  if (closeOnSameLine >= 0) {
    // $$E = mc^2$$
    content = firstLine.slice(0, closeOnSameLine)
  } else {
    // 开标记必须独占一行，否则 "$$ 价格" 这种正文会被吞掉。
    if (firstLine.trim() !== '') return false
    const from = state.bMarks[startLine + 1]
    let closed = -1
    for (let line = startLine + 1; line < endLine; line += 1) {
      const lineStart = state.bMarks[line] + state.tShift[line]
      const lineEnd = state.eMarks[line]
      if (state.src.slice(lineStart, lineEnd).trim() === '$$') {
        closed = line
        break
      }
    }
    if (closed < 0) return false
    content = state.src.slice(from, state.bMarks[closed])
    nextLine = closed + 1
  }

  if (silent) return true

  const token = state.push('math_block', 'math', 0)
  token.block = true
  token.content = content.trim()
  token.map = [startLine, nextLine]
  state.line = nextLine
  return true
}

/* ------------------------------------------------------------------ */
/* 引用编号：[3]。只有渲染时能对上 sources 的编号才做成按钮。              */
/* ------------------------------------------------------------------ */

const CITATION_PATTERN = /^\[(\d{1,3})\]/

function citationRule(state: StateInline, silent: boolean): boolean {
  if (state.src.charCodeAt(state.pos) !== 0x5b /* [ */) return false
  const match = CITATION_PATTERN.exec(state.src.slice(state.pos, state.posMax))
  if (!match) return false

  const after = state.src.charCodeAt(state.pos + match[0].length)
  // 后面是 ( 说明这是一个 Markdown 链接，不是引用；是 [ 则是引用式链接。
  if (after === 0x28 || after === 0x5b) return false

  if (silent) return true
  const token = state.push('citation', '', 0)
  token.content = match[1]
  state.pos += match[0].length
  return true
}

/* ------------------------------------------------------------------ */
/* 实例与渲染规则                                                       */
/* ------------------------------------------------------------------ */

function escapeHtml(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

function highlightCode(code: string, language: string): string {
  const name = language.toLowerCase()
  if (!name || !hljs.getLanguage(name)) return escapeHtml(code)
  try {
    return hljs.highlight(code, { language: name, ignoreIllegals: true }).value
  } catch {
    return escapeHtml(code)
  }
}

const md = new MarkdownIt({
  html: false,
  linkify: true,
  breaks: true,
  highlight: highlightCode,
})

md.inline.ruler.before('escape', 'math_inline', mathInlineRule)
md.block.ruler.before('fence', 'math_block', mathBlockRule)
md.inline.ruler.before('link', 'citation', citationRule)

md.renderer.rules.math_inline = (tokens, index) => {
  const source = tokens[index].content
  try {
    return katex.renderToString(source, {
      throwOnError: false,
      trust: false,
      output: 'html',
      displayMode: false,
    })
  } catch {
    return `<code>${escapeHtml(source)}</code>`
  }
}

md.renderer.rules.math_block = (tokens, index) => {
  const source = tokens[index].content
  try {
    return `<div class="math-block">${katex.renderToString(source, {
      throwOnError: false,
      trust: false,
      output: 'html',
      displayMode: true,
    })}</div>\n`
  } catch {
    return `<pre><code>${escapeHtml(source)}</code></pre>\n`
  }
}

md.renderer.rules.citation = (tokens, index, _options, env) => {
  const number = tokens[index].content
  const known = (env as RenderContext).citations
  if (!known || !known.has(Number(number))) {
    // 对不上来源的编号不是错误，只是普通文本 —— 不给一个点了没反应的按钮。
    return escapeHtml(`[${number}]`)
  }
  return (
    `<button type="button" class="citation" data-citation="${escapeHtml(number)}"` +
    ` title="查看引用 ${escapeHtml(number)}">${escapeHtml(number)}</button>`
  )
}

md.renderer.rules.image = (tokens, index) => {
  const token = tokens[index]
  const source = String(token.attrGet('src') ?? '')
  const alt = token.content || String(token.attrGet('alt') ?? '') || '图片'
  // 不加载远程图片：模型给的地址既不可信，也会造成追踪像素。
  if (!/^https?:\/\//i.test(source)) return escapeHtml(alt)
  return (
    `<a href="${escapeHtml(source)}" target="_blank" rel="noopener noreferrer nofollow">` +
    `${escapeHtml(alt)}</a>`
  )
}

// 外链统一新窗口打开，并切断 opener。
const defaultLinkOpen = md.renderer.rules.link_open
md.renderer.rules.link_open = (tokens, index, options, env, self) => {
  const token = tokens[index]
  token.attrSet('target', '_blank')
  token.attrSet('rel', 'noopener noreferrer nofollow')
  if (defaultLinkOpen) return defaultLinkOpen(tokens, index, options, env, self)
  return self.renderToken(tokens, index, options)
}

const SANITIZE_OPTIONS = {
  ALLOWED_TAGS: [
    'p',
    'br',
    'hr',
    'strong',
    'em',
    'del',
    's',
    'code',
    'pre',
    'blockquote',
    'ul',
    'ol',
    'li',
    'a',
    'h1',
    'h2',
    'h3',
    'h4',
    'h5',
    'h6',
    'table',
    'thead',
    'tbody',
    'tr',
    'th',
    'td',
    'span',
    'div',
    'sup',
    'sub',
    'button',
  ],
  ALLOWED_ATTR: [
    'href',
    'title',
    'class',
    'style',
    'target',
    'rel',
    'start',
    'colspan',
    'rowspan',
    'type',
    'aria-hidden',
    'data-citation',
  ],
  ALLOWED_URI_REGEXP: /^(?:https?:|mailto:|tel:|#)/i,
  FORBID_TAGS: ['script', 'iframe', 'object', 'embed', 'style', 'svg', 'math', 'form', 'input'],
  FORBID_ATTR: ['srcset', 'formaction', 'xlink:href'],
}

export function renderMarkdown(source: string, context: RenderContext = {}): string {
  const html = md.render(source, context)
  return DOMPurify.sanitize(html, SANITIZE_OPTIONS)
}
