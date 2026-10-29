/** 掌握路径的文案。 */

export const MASTERY = {
  title: '掌握路径',
  description: '每个知识点分未接触、薄弱、学习中、已掌握四态；下一步都带理由与证据。',
  /** 一级页（选路径）的说明。二级页沿用路径自己的说明。 */
  indexDescription: '选一条路径进入总览；路径由一次路径对话生成。',
  backToPaths: '回到路径',
  pathNoDescription: '这条路径还没有说明。',

  paths: '路径',
  pathsEmpty: '还没有掌握路径',
  pathsEmptyHint: '掌握路径由一次对话生成。',
  pathsFailed: '路径列表没能读出来',
  /** 掌握路径页的动作按结果命名：按下之后得到的就是一条路径。 */
  newPath: '新建路径',
  /** 学习地图的空态仍按去处命名：它就在地图上，说的是下一步去哪。 */
  goChat: '去路径对话新建',
  goChatHint: '在路径对话里说明想学什么，使用 mastery_path 能力生成一条路径。',
  capabilityMissing: '当前能力清单里没有 mastery_path，暂时不能在路径对话里生成新路径。',

  /**
   * 路径对话的第一级：对话历史。
   *
   * 这一页是只读的历史，不提供「新建」：新路径从「掌握路径」与「学习地图」
   * 的「去路径对话新建」直接开一段会话，第一句落进去时后端才建路径。
   */
  chatHistoryTitle: '路径对话',
  chatHistoryDescription:
    '每段对话创建一条路径，路径名和名字下那句说明都由第一轮对话总结出来。点一行回到那段对话继续；要新开一条，从「掌握路径」或「学习地图」进。',
  chatHistoryEmpty: '还没有路径对话',
  chatHistoryEmptyHint: '到「掌握路径」点「去路径对话新建」，说一句想学什么，第一轮会生成一条路径。',
  chatHistoryFailed: '路径对话没能读出来',
  chatHistoryRetry: '重试',
  chatHistoryPath: '路径',
  chatHistoryUntitled: '还没有标题',
  chatHistoryOpen: '继续这段对话',
  /** 开新对话与回到旧对话失败时留在页面上的话（`shared/masteryChat.ts`）。 */
  chatCreateFailed: '新对话没能建起来，请重试',
  chatOpenFailed: '这段路径对话没能打开，请重试',
  chatOpenMissing: '这条路径还没有对话，回路径对话历史看看',

  /** 路径对话页（挂在掌握路径下）。 */
  chatTitle: '掌握路径对话',
  chatBackToHistory: '返回对话历史',
  chatLead: '说说你想学什么。这一页只跑掌握路径：把内容拆成知识点，出题判卷，按证据推进。',
  chatPrompts: [
    '我想系统学离散数学',
    '帮我梳理线性代数的知识地图',
    '沿着上次的掌握路径继续',
  ],
  chatHint: '第一次发送会新建一条路径对话；想接着上次的路径，直接说「继续」。',
  chatPlaceholder: '说说想学什么，或者接着当前路径继续…',
  chatListTitle: '路径对话',
  chatListEmptyAction: '开始一段路径对话',
  chatNewLabel: '新建对话',
  chatEmptySession: '这段路径对话还没有消息',
  chatEmptySessionHint: '说明想学什么，或者接着当前路径继续。',
  chatEmptySessionAction: '回到首页',

  /** 出题弹窗。模型用 mastery_question 出题后，题目抬到眼前作答。 */
  questionTitle: '做一道题',
  questionChoiceHint: '选好后提交；题目要求多选时，可以选多个。',
  questionInputPlaceholder: '写下你的答案',
  questionShortPlaceholder: '写下你的作答，可以分几行',
  questionOffline: '连接没就绪，暂时不能提交；连上之后再作答。',
  questionSubmit: '提交答案',
  questionLater: '稍后作答',
  questionReopen: '继续作答',
  /** 出题调用成功，题目刚落进题库。 */
  questionBankSaved: '这道题已存进题库。',
  /** 调用失败，但按题干在题库里核对到了同一道题。 */
  questionBankFound: '题库里已有这道题。',
  /** 调用失败，题库里也找不到；题面仍可作答。 */
  questionBankMissing: '这道题没能存进题库，暂时没有可判卷的记录；你仍然可以作答。',
  /** 核对请求本身失败，不对题库下结论。 */
  questionBankUnknown: '没能确认题库里是否已有这道题；你仍然可以作答。',

  goal: '目标',
  status: '状态',
  version: '版本',
  updatedAt: '更新于',
  neverSeen: '从未看过',
  lastSeen: '上次查看',
  modules: (count: number) => `${count} 个模块`,
  points: (count: number) => `${count} 个知识点`,
  masteredCount: (done: number, total: number) => `已掌握 ${done} / ${total}`,
  detailFailed: '这条路径没能读出来',
  insightFailed: '掌握情况没能读出来',
  retry: '重试',

  levelUnknown: '未接触',
  levelWeak: '薄弱',
  levelLearning: '学习中',
  levelMastered: '已掌握',
  levelLabel: '掌握状态',

  modulesTitle: '模块与知识点',
  moduleEmpty: '这条路径还没有知识点',
  /** 模块在时间线上的三态，按它自己的知识点算出来。 */
  moduleDone: '已完成',
  moduleActive: '进行中',
  moduleTodo: '未开始',
  progress: (percent: number) => `进度 ${percent}%`,
  pointEmpty: '选一个知识点，这里会显示它的观察记录与证据。',
  pointStats: '观察统计',
  totalObservations: (count: number) => `共 ${count} 次观察`,
  correctCount: (count: number) => `正确 ${count}`,
  incorrectCount: (count: number) => `错误 ${count}`,
  partialCount: (count: number) => `部分正确 ${count}`,
  lastObserved: '上次观察',
  dueAt: '下次复习',
  noObservation: '还没有观察记录',

  nextTitle: '下一步',
  nextMap: '整理路径',
  nextLearn: '学习',
  nextReview: '复习',
  nextDone: '已完成',
  nextEvidence: (count: number) => `${count} 条证据`,
  nextMissingEvidence: (count: number) => `另有 ${count} 条证据不在这次返回里，没有画出来。`,
  nextEvidenceMissing: (id: string) => `证据 ${id}（未随这次结果返回）`,
  nextNoEvidence: '这一步没有附证据。',
  nextGoPractice: '去练习',
  nextGoChat: '去路径对话',

  evidenceTitle: '证据',
  evidenceEmpty: '这条路径还没有证据',
  evidenceFailed: '证据没能读出来',
  evidenceOf: (name: string) => `${name} 的证据`,
  allEvidence: '全部证据',
  resultCorrect: '正确',
  resultIncorrect: '错误',
  resultPartial: '部分正确',
  evidenceSource: '来源',
  evidenceQuality: '质量',
  evidenceDetail: '原始记录',
  evidenceVisit: '看这个知识点',

  leaseTitle: '写权限',
  leaseHeld: '另一个会话正在修改这条路径。',
  leaseHolder: (sessionId: string) => `会话 ${sessionId}`,
  leaseTurn: (turnId: string) => `轮次 ${turnId}`,
  leaseAcquired: '开始于',
  leaseWait: '刷新状态',
  leaseRelease: '让对方停下',
  leaseReleasing: '正在释放…',
  leaseReleaseFailed: '没能让对方停下',
  leaseReleased: '对方的写权已释放',
  leaseExplanation: '释放只会让另一个会话停下，不会把写权交给当前页面。写权只能通过一次 mastery_path 对话取得。',
  leaseConfirmTitle: '让对方停下？',
  leaseConfirmBody: '另一个会话正在修改这条路径。这个动作会中断它的写入，但不会把写权交给这里。',
  leaseConfirmNote: '这个动作会记进审计。',
  leaseFailed: '写权限状态没能读出来',
} as const
