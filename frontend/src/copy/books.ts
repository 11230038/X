/**
 * 活书的文案。
 *
 * 这里的每一句都对应界面上一个真实动作或一种真实状态。尤其是"生成"：
 * 一次请求就是一次模型调用，可能跑两分钟，所以按钮上要说清正在做什么，
 * 而不是只转一个圈。
 *
 * 错误码 → 一句话单独放在 `BOOK_ERRORS`：同一个 `outline_locked` 在详情页
 * 与生成循环里都必须给出同一个解释，写在两处就会有一天只改了一处。
 */

import type { BookSourceKind, ChapterStatus } from '../api/books'

export const BOOKS = {
  title: '活书',
  description: '把一段对话、一个知识库、一份文件或题库，生成一本可以按章阅读的书。',

  /* ── 书架 ─────────────────────────────────────────────────────────── */

  newBook: '新建活书',
  loadFailed: '书架没能读出来',
  refreshFailedKept: '这一次刷新没有成功，下面显示的是上一次读到的书架。',
  emptyTitle: '还没有活书',
  emptyHint: '选一个来源，先让模型写一份大纲；确认之后，一章一章生成。',
  emptyAction: '新建第一本',
  emptySecondary: '去文件库',
  count: (count: number) => `${count} 本`,
  sourceSnapshot: '创建时的来源快照',
  chapters: (ready: number, total: number) => `${ready}/${total} 章`,
  notStarted: '还没开始读',
  progressAt: (ordinal: number) => `读到第 ${ordinal} 章`,
  updatedAt: (time: string) => `更新于 ${time}`,
  openBook: '打开',

  /* ── 新建 ─────────────────────────────────────────────────────────── */

  createTitle: '新建活书',
  createHint: '来源会在创建时保存一份快照；之后大纲与每一章都从这份快照生成。',
  titleLabel: '书名（可选）',
  titlePlaceholder: '默认使用来源标题',
  chooseSession: '选一段对话',
  chooseKnowledgeBase: '选一个知识库',
  chooseFile: '选一份文件',
  questionBankNote: '题库不需要选择：这份书会从你题库里的全部题目生成。',
  sourceListEmpty: '这个来源下还没有可选的项',
  sourceListFailed: '来源列表没能读出来',
  unparsedFile: '这个文件还没有解析。先把它加入一个知识库，再回来新建。',
  loadingFile: '正在检查这份文件…',
  needSource: '先选一个来源',
  creating: '正在创建…',
  create: '创建活书',
  cancel: '取消',
  createFailed: '这本书没能创建',
  noSessionOption: '还没有对话',
  noKnowledgeBaseOption: '还没有知识库',
  noFileOption: '文件库里还没有文件',
  goChat: '去对话',
  goKnowledge: '去知识库',
  goFiles: '去文件库',

  /* ── 详情 ─────────────────────────────────────────────────────────── */

  backToShelf: '返回书架',
  detailLoadFailed: '这本书没能读出来',
  detailRefreshFailedKept: '这一次刷新没有成功，下面显示的是上一次读到的内容。',
  delete: '删除',
  deleting: '正在删除…',
  deleteTitle: (title: string) => `删除《${title}》？`,
  deleteBody: '这本书的大纲与已经生成的正文都会一起删除，不能恢复。来源本身不受影响。',
  deleteConfirm: '删除这本书',
  deleted: '活书已删除',
  deleteFailed: '这本书没能删除',

  sourceOf: (kind: string, title: string) => (title ? `${kind}《${title}》` : kind),
  sourceChars: (count: number) => `来源快照 ${count} 字`,
  sourceTruncated: '来源比上限长，只保存了前面的部分',
  outlineLocked: '已经生成过正文，大纲不能再改。',
  unknownSource: '未知来源',

  /* ── 大纲 ─────────────────────────────────────────────────────────── */

  outlineEmpty: '还没有大纲',
  outlineEmptyHint: '让模型先写一份目录；你可以改标题、增删章节，再确认。',
  generateOutline: '生成大纲',
  generatingOutline: '正在生成大纲（最长约 2 分钟）…',
  regenerateOutline: '重新生成大纲',
  regenerateTitle: '重新生成大纲？',
  regenerateBody: '当前的草稿会被模型写的新大纲替换；还没生成的章节会跟着换掉。',
  regenerateConfirm: '重新生成',
  outlineDraft: '大纲草稿',
  outlineDraftHint: '顺序就是章节顺序。确认之后才能生成正文。',
  outlineConfirmed: '大纲已确认',
  outlineConfirmedHint: '已经生成过正文，大纲不能再改；可以逐章生成或重新生成某一章。',
  chapterTitle: '章节标题',
  chapterSummary: '一句话说明这一章写什么',
  addChapter: '添加一章',
  removeChapter: '删除',
  moveUp: '上移',
  moveDown: '下移',
  saveOutline: '保存大纲',
  saveAndConfirm: '保存并确认大纲',
  savingOutline: '正在保存…',
  outlineSaveFailed: '大纲没能保存',
  outlineGenerateFailed: '大纲没能生成',
  chapterNumber: (ordinal: number) => `第 ${ordinal} 章`,
  outlineTooMany: '最多 12 章',
  outlineTooFew: '至少要有一章',
  outlineTitleEmpty: '章节标题不能是空的',
  outlineTitleLong: '章节标题最多 120 字',
  outlineSummaryLong: '章节说明最多 500 字',
  outlineDuplicate: (title: string) => `「${title}」重复了`,

  /* ── 生成与阅读 ───────────────────────────────────────────────────── */

  chaptersTitle: '章节',
  chapterPending: '未生成',
  chapterReady: '已生成',
  chapterFailed: '生成失败',
  chapterGenerating: '正在生成',
  selectChapter: '选一章开始阅读',
  selectChapterHint: '左边选一章；已经生成的正文会显示在这里。',
  generateChapter: '生成本章',
  retryChapter: '重试本章',
  generatingChapter: (ordinal: number) => `正在生成第 ${ordinal} 章（最长约 2 分钟）…`,
  chapterLoadFailed: '这一章没能读出来',
  chapterGenerateFailed: '这一章没能生成',
  chapterPendingHint: '这一章还没有正文。',
  chapterFailedHint: '上一次生成没有成功；可以重试，已经生成的其它章不受影响。',
  generateRemaining: '生成剩余章节',
  generatingRemaining: (done: number, total: number) => `正在生成：已完成 ${done}/${total}`,
  stopGeneration: '停止',
  batchStopped: (done: number, total: number, ordinal: number) =>
    `已生成 ${done}/${total} 章，第 ${ordinal} 章停下来。可以继续生成，或单独重试这一章。`,
  batchPaused: (done: number, total: number) => `已停止：目前生成到 ${done}/${total} 章。`,
  batchDone: (total: number) => `全部 ${total} 章都已生成。`,
  batchQuota: '额度用完，生成停在当前这一章；等额度重置后继续。',
  batchRateLimited: '请求太频繁，生成停在当前这一章；稍后再继续。',
  batchInFlight: '这本书已经有一个生成请求在跑，等它结束再继续。',
  batchOutlineChanged: '大纲在生成过程中换了版本，这一章的正文没有保存。刷新后再试。',
  exportChapters: '导出已生成章节',
  exportAll: '导出这本书',
  exporting: '正在导出…',
  exportEmpty: '一章都还没生成，暂时没有可以导出的内容。',
  exportFailed: '这本书没能导出',
  exported: '已开始下载',

  /* ── 目标 ─────────────────────────────────────────────────────────── */

  statusLabel(status: ChapterStatus): string {
    if (status === 'ready') return BOOKS.chapterReady
    if (status === 'failed') return BOOKS.chapterFailed
    return BOOKS.chapterPending
  },

  statusTone(status: ChapterStatus): 'success' | 'danger' | 'neutral' {
    if (status === 'ready') return 'success'
    if (status === 'failed') return 'danger'
    return 'neutral'
  },

  sourceLabel(kind: BookSourceKind): string {
    switch (kind) {
      case 'session':
        return '对话'
      case 'knowledge_base':
        return '知识库'
      case 'file':
        return '文件'
      case 'question_bank':
        return '题库'
      default:
        return BOOKS.unknownSource
    }
  },

  defaultTitle(kind: BookSourceKind): string {
    switch (kind) {
      case 'session':
        return '未命名对话'
      case 'knowledge_base':
        return '知识库'
      case 'file':
        return '文件'
      case 'question_bank':
        return '我的题库'
      default:
        return '活书'
    }
  },
} as const

/**
 * 后端错误码在这一块里的固定出口。
 *
 * 认不出的码仍然交给 `describeFailure`（它至少会说一句中性的话），这里只
 * 负责那些"换一句话说就是另一件事"的码。
 */
export const BOOK_ERRORS: Record<string, string> = {
  source_empty: '这个来源没有可用的文字，换一个来源。',
  source_unavailable: '来源读不到了：它可能已被删除，或者文件还没有解析。',
  outline_not_confirmed: '先保存并确认大纲，才能生成正文。',
  outline_locked: '已经生成过正文，大纲不能再改。删掉这本书重建，或继续生成剩下的章节。',
  outline_changed: '大纲在生成过程中换了版本，这一章的正文没有保存。重新生成这一章。',
  generation_in_flight: '这本书已经有一个生成请求在跑，等它结束再试。',
  no_ready_chapters: '一章都还没生成，暂时没有可以导出的内容。',
  generation_unavailable: '这个部署没有配置模型，暂时不能生成。',
  generation_failed: '模型这次没有写出可用的内容，可以再试一次。',
  invalid_outline: '大纲不合法：检查章节数、标题与重名。',
  invalid_progress: '阅读进度超出了大纲范围，刷新后再试。',
  not_found: '这本书或这一章已经不在了。',
}
