/** 资料市场的文案。 */

export const FILES = {
  title: '资料市场',
  description: '上传、解析、下载与回收站。上传不代表能读，解析完成才代表。',

  active: '在用',
  deleted: '回收站',
  searchPlaceholder: '按文件名搜索',
  search: '搜索',
  clearSearch: '清除搜索',

  upload: '上传文件',
  uploadTitle: '上传文件',
  dropTitle: '把文件拖到这里',
  dropHint: '或点击选择文件',
  close: '关闭',
  uploading: '正在上传',
  cancelUpload: '取消上传',
  uploadOneAtATime: '一次上传一个文件',

  emptyActive: '资料市场还是空的',
  emptyActiveHint: '上传一份资料，解析之后就能在对话里引用它。',
  emptyDeleted: '回收站是空的',
  emptyDeletedHint: '在这里的会话与文件都可以恢复；永久删除才不可逆。',
  emptySearch: '没有匹配的文件',
  emptySearchHint: '换一个文件名再试，或者清除搜索看全部。',

  /** 卡片右下那枚按钮：卡片本来是整块可点的，这枚只是把"点开"说清楚。 */
  cardAction: '查看详情',

  unparsedTitle: '已上传，尚未解析',
  unparsedHint: '这个文件已经存下来了，但还没有被解析成可检索的文本。把它加入一个知识库就会开始解析。',
  addToKnowledgeBase: '加入知识库',
  chooseKnowledgeBase: '选择知识库',
  addToKnowledgeBaseAction: '加入',
  noKnowledgeBases: '还没有知识库',
  createKnowledgeBaseLink: '去新建一个知识库',
  addedToKnowledgeBase: '已加入知识库，解析已开始',

  detail: '文件信息',
  filename: '文件名',
  size: '大小',
  type: '类型',
  category: '分类',
  uploadedAt: '上传时间',
  checksum: '校验值',
  document: '解析结果',
  engine: '解析器',
  textChars: '文本字数',
  chunks: '分块',
  vectorState: '向量状态',
  parsedAt: '解析时间',
  truncated: '原文过长，解析时已截断',
  chunkSize: '分块大小',
  chunkOverlap: '分块重叠',

  download: '下载',
  downloadFailed: '下载没能完成，请重试',
  remove: '移入回收站',
  restore: '恢复',
  purge: '永久删除',
  removed: '已移入回收站',
  restored: '已恢复',
  purged: '已永久删除',

  removeTitle: '把这个文件移入回收站？',
  removeBody: '文件会从在用列表里消失，可以随时恢复。',
  removeNote: '知识库里的引用不受影响。',
  removeConfirm: '移入回收站',
  purgeTitle: '永久删除这个文件？',
  purgeBody: '文件本身与它的解析结果都会被删除。',
  purgeNote: '这一步不能撤销。',
  purgeConfirm: '永久删除',
  purgeInUseTitle: '还有东西在用它，暂时不能删除',
  purgeInUseHint: '先从下面这些地方移除引用，再回来删除。',
  purgeReferences: '引用方',

  textTitle: '文件文本',
  textPending: '解析完成后才能查看文本。',
  textLoadMore: '继续加载',
  textRange: (from: number, to: number, total: number) =>
    `第 ${from}–${to} 字，共 ${total} 字`,
} as const

/** 永久删除被拒时，`details.references[].Kind` 的两种取值。 */
export const REFERENCE_KIND_LABELS: Record<string, string> = {
  document: '知识库文档',
  reading_material: '阅读材料',
}

export const VECTOR_STATE_LABELS: Record<string, string> = {
  pending: '等待向量化',
  running: '正在向量化',
  ready: '向量就绪',
  failed: '向量化失败',
}

export const VECTOR_STATE_TONES: Record<
  string,
  'neutral' | 'accent' | 'success' | 'warning' | 'danger'
> = {
  pending: 'neutral',
  running: 'accent',
  ready: 'success',
  failed: 'danger',
}

/** 上传类失败的出口。查不到的码才用后端 message。 */
export const UPLOAD_ERRORS: Record<string, string> = {
  request_too_large: '整个请求太大，换一个小一点的文件',
  file_too_large: '这个文件超过了单文件大小上限',
  file_required: '没有选到文件，请重新选择',
  multiple_files: '一次只能上传一个文件',
  invalid_file: '这个文件无法上传，内容可能已经损坏',
  invalid_filename: '文件名不合法，请改名后重试',
  unsupported_file_type: '这个文件类型目前不支持',
  file_type_mismatch: '文件内容和扩展名对不上，请检查后重试',
  invalid_multipart: '上传请求格式不对，请刷新页面重试',
  unexpected_multipart_field: '上传请求里带了多余字段，请刷新页面重试',
  upload_failed: '上传没能完成，请重试',
}

export const FILE_ACTION_ERRORS: Record<string, string> = {
  not_found: '这个文件已经不在了，刷新列表看看',
  not_parsed: '这个文件还没有被解析',
  invalid_request: '这个文件编号不被接受，刷新列表再试',
  file_in_use: '还有东西在用它，暂时不能删除',
  library_unavailable: '资料市场暂时不可用，请稍后重试',
}
