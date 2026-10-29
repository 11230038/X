/** 个人中心的文案。版式照演示稿的 `#page-profile`。 */

export const PROFILE = {
  title: '个人中心',
  description: '账号、学习记录与登录状态都在这里。',

  /**
   * 演示稿头像底下那句自我介绍。这一版没有编辑资料的接口，所以它不是用户
   * 数据，只是这一页固定的开场白 —— 不画"编辑资料"按钮。
   */
  bio: '在 Rinnex 探索知识的边界，保持好奇。',

  roleAdmin: '管理员',
  roleUser: '普通账号',

  /** 演示稿的三项是陪伴天数 / 已学知识点 / 本周学习，这里只放统计接口算得出的。 */
  statReviews: '累计复习',
  statCorrect: '正确回答',
  statAccuracy: '准确率',
  statPending: '—',

  favorites: '我的收藏',
  favoritesHint: '沉浸阅读的段落与题库的题目',
  questions: '题库',
  questionsHint: '所有题目与分类',
  practice: '练习',
  practiceHint: '复习队列与学习记录',

  accountSecurity: '账号与安全',
  accountSecurityHint: '本地账号 · 修改密码',
  settings: '设置',
  settingsHint: '记忆、伙伴、技能、工具与系统设置',

  accountSecurityTitle: '账号与安全',
  accountSecurityDescription: '账号信息、修改密码与退出登录都在这里。',

  /** 账号与安全子页（`/profile/account`）的几处措辞。 */
  accountIdLine: (id: string | number) => `用户编号 ${id}`,
  accountNoEmail: '这个版本不提供邮箱，也没有找回密码。',
  accountPreset: '预设',
  confirmPassword: '再输一次新密码',
  passwordRule: '8–72 字节，区分大小写。',
  changePasswordNote: '改完密码后，所有设备上的登录都会失效，需要重新登录。',
  passwordSubmitting: '正在修改…',
  passwordNeedBoth: '当前密码与新密码都不能为空',
  passwordLength: '新密码长度需要在 8–72 字节之间',
  passwordMismatch: '两次输入的新密码不一致',
  signOutAction: '退出登录',
  signOutBusy: '正在退出…',
  signOutConfirmNote: '本地保存的登录凭据会一并清掉，之后只能用用户名和密码重新登录。',

  /**
   * 收藏夹（演示稿 `#page-favorites`）。两类收藏共用一张卡片：原文是引文，
   * 题目是题干；各自回到收藏它的地方。
   */
  favoritesTitle: '收藏夹',
  favoritesDescription: '你收藏的原文与题目都在这里；点一张卡片，回到它所在的那一页。',
  favoritesKind: '书籍片段',
  favoritesQuestionKind: '题目',
  favoritesQuestionSource: '题库',
  favoritesFrom: '来自',
  favoritesEmpty: '还没有收藏',
  favoritesEmptyHint: '在沉浸阅读里选中一段点「收藏」，或在题库里给一道题点收藏。',
  favoritesFailed: '收藏没能读出来',
  favoritesReadingFailed: '阅读里的收藏没能读出来',
  favoritesQuestionsFailed: '题库里的收藏没能读出来',
  favoritesSnippet: (ordinal: number) => `第 ${ordinal + 1} 段`,
  favoritesUnknownMaterial: '未知材料',
  favoritesGoReading: '去沉浸阅读',
  favoritesGoQuestions: '去题库',
} as const
