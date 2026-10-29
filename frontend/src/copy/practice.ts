/** 练习与统计的文案。 */

export const PRACTICE = {
  title: '练习',
  description: '按复习队列逐题作答。客观题看判定结果，主观题看 AI 的判断。',

  queueProgress: (index: number, total: number) => `第 ${index} / ${total} 题`,
  reasonDue: '到期复习',
  reasonNew: '新题',
  mistake: '错题',

  answerPlaceholder: '写下你的答案',
  choiceHint: '选一个选项',
  submit: '提交作答',
  submitting: '正在判分…',
  skip: '跳过这题',
  skipping: '正在跳过…',
  next: '下一题',
  finish: '完成练习',
  cancel: '取消',

  /** 题目旁：不会就先记成答错，再去题目问答里问它。 */
  askAbout: '我不会，去提问',
  asking: '正在记成答错…',
  askFailed: '这道题没有记成答错：',

  objectiveTitle: '判定结果',
  modelTitle: 'AI 的判断',
  selfReportTitle: '你的声明',
  correct: '回答正确',
  incorrect: '回答不正确',
  confidence: '置信度',
  rubric: '评分标准',
  feedback: '反馈',
  suggestion: '建议',
  boundary: '边界',
  model: '模型',
  replayed: '这次提交是一次重放，结果与第一次相同。',
  skippedTitle: '这题已跳过',
  skippedHint: '跳过不会判分，也不会改变复习安排。',

  emptyQueue: '现在没有要复习的题',
  emptyQueueHint: '去题库里挑几道题，或者等复习时间到了再来。',
  goQuestions: '去题库',
  backToQuestions: '回到题库',

  judgeUnavailable: '判分服务暂时不可用。你的作答还在，稍后可以原样重试。',
  notJudgeable: '这道题暂时无法判分。作答没有被保存，可以跳过它。',
  staleReview: '这道题在别处被复习过，版本已经更新。确认后可以按最新状态重新提交。',
  adoptLatest: '按最新状态重新提交',
  conflictingRequest: '这次提交的编号已经被别的作答用过，已换一个新的编号，请再提交一次。',
  failed: '提交没有完成',
  retry: '重试',

  statsTitle: '练习统计',

  /** 面板里唯一的一张卡。只写统计接口拿得到的三项；平均用时与自评目前没有数据。 */
  recordTitle: '你的学习记录',
  recordReviews: '累计复习次数',
  recordResult: '正确 / 错误',
  recordAccuracy: '准确率',
  recordTimes: (count: number) => `${count} 次`,
  recordQuote:
    '学过多少、复习了什么、哪题还不稳，都能留下痕迹。不是凭感觉学，而是让复习过程有记录、有反馈。',
  statsEmpty: '还没有复习记录，做过几道题之后这里就会有数。',
  statsFailed: '统计没能读出来',

  /** 面板的两个 Tab：复习轨迹（记录卡）与错题解析。 */
  tabTracker: '复习轨迹',
  tabAnalysis: '错题解析',

  analysisTitle: '错题解析助手',
  analysisTarget: '正在解析',
  analysisAsk: '解析这道题',
  analysisAgain: '重新解析',
  /** 解析里说得出来答案：作答之前按钮禁用，这句说明为什么。 */
  analysisHint: '解析里会说出答案。',
  analysisLocked: '先作答这道题，再做解析——解析里会说出答案。',
  analysisLoading: '正在解析，稍等片刻。',
  analysisEmpty: '现在没有要复习的题，解析也就无从谈起。',
  analysisFailed: '解析没能出来',
} as const
