/** 题库的文案。 */
import type { AppIconName } from '../components/common/AppIcon.vue'

export const QUESTIONS = {
  title: '题库',
  description: '导入、筛选与复习。导入先预览逐行结果，确认之后才写入。',

  /** 标题下面一排能力标签，照演示稿错题本页的 `.f-pill`。别写做不到的事。 */
  featurePills: [
    { icon: 'search', label: '支持搜索题目' },
    { icon: 'tag', label: '按题型 / 分类筛选' },
    { icon: 'space', label: '掌握状态清楚' },
    { icon: 'eye', label: '复习状态一目了然' },
  ] satisfies readonly { icon: AppIconName; label: string }[],

  searchPlaceholder: '按题目内容搜索',
  filterResolved: '掌握状态',
  filterType: '题型',
  filterCategory: '分类',
  filterPath: '掌握路径',
  pathFallback: (pathId: string) => `掌握路径 ${pathId}`,
  all: '全部',
  resolvedUntouched: '未处理过',
  resolvedOpen: '未掌握',
  resolvedDone: '已掌握',

  typeChoice: '选择题',
  typeFillBlank: '填空题',
  typeShortAnswer: '简答题',

  empty: '题库还是空的',
  emptyHint: '导入一段 JSON Lines，或者先让对话替你生成题目。',
  emptyFiltered: '没有符合这些条件的题',
  emptyFilteredHint: '放宽一个筛选条件，或者清除搜索再看。',
  clearFilters: '清除筛选',
  /** 列表态的名字：详情页的返回按钮要回到这里。 */
  selectTitle: '选择题目',

  importAction: '导入题目',
  importTitle: '导入题目',
  importHint: '每行一道题的 JSON Lines。先预览，确认后才会写入。',
  importPlaceholder: '{"text":"…","question_type":"choice","options":[…],"correct_answer":"A"}',
  importPreview: '预览',
  importCommit: '确认导入',
  importCancel: '取消',
  importDone: '完成',
  importEmpty: '先粘贴要导入的内容',
  importPreviewTitle: '预览结果',
  importResultTitle: '导入结果',
  importSummary: (lines: number, writable: number) => `共 ${lines} 行，其中 ${writable} 行可以写入`,
  importSucceeded: (count: number) => `已写入 ${count} 道题`,
  importPreviewFailed: '预览没能完成',
  importCommitFailed: '导入没能完成',
  importAgain: '重新预览',
  lineNumber: (line: number) => `第 ${line} 行`,
  outcomeWritable: '可写入',
  outcomeMissingAnswer: '缺少答案',
  outcomeDuplicate: '重复',
  outcomeInvalid: '无效',
  conflictWith: (id: string, line: number) => `与第 ${line} 行的 ${id} 冲突`,

  askAbout: '提问',
  bookmark: '收藏',
  bookmarked: '已收藏',
  markMastered: '标为已掌握',
  markOpen: '标为未掌握',
  unmark: '清除标记',
  resolvedMastered: '已掌握',
  correct: '回答正确',
  incorrect: '回答不正确',
  attempts: (count: number) => `作答 ${count} 次`,
  dueAt: '下次复习',
  lastAnswered: '上次作答',
  neverAnswered: '还没有作答过',

  questionDetail: '题目',
  answerDetail: '你的作答',
  correctAnswer: '参考答案',
  explanation: '解析',
  difficulty: '难度',
  source: '来源',
  categories: '分类',
  knowledgePoints: '知识点',
  noExplanation: '这道题没有解析。',
  actionFailed: '这一步没能完成，请重试',
  loadFailed: '题目列表没能读出来',
  detailFailed: '这道题没能读出来',
  options: '选项',
} as const

/**
 * 题目问答：题库自己的对话页。
 *
 * 单独一份，是因为它不是题库列表的一部分：这个页面先在题库里挑一道题，再就
 * 这道题建立一段对话。文案放在这里而不是 `chat.ts`，与 `MASTERY` 的路径对话
 * 文案放在掌握路径下是同一条规矩。
 */
export const QUESTION_CHAT = {
  title: '题目问答',
  searchPlaceholder: '按题目内容搜索',
  loadFailed: '题目列表没能读出来',
  retry: '重试',
  empty: '题库还是空的',
  emptyHint: '先在题库页导入题目，或者在对话里让助手出题。',
  noMatch: '没有找到匹配的题目',
  noMatchHint: '换一个关键词再看。',
  resultLimit: (count: number) => `只显示前 ${count} 道；没看到要找的题就搜一下。`,
  /** 输入区里的按钮与它打开的弹窗。 */
  pickQuestion: '选择题目',
  pickerTitle: '选一道题',
  pickerClose: '关闭',
  pickerLoading: '正在读取…',
  /** 从练习页带着题号过来、但这个题号没能读到时。 */
  preselectFailed: '没能自动选中这道题，请选择或搜索它。',
  defaultAsk: '请先讲解这道题，并指出最容易错的地方。',
  composeQuestion: '题目',
  composeType: '题型',
  composeOptions: '选项',
  composeAnswer: '参考答案',
  composeExplanation: '解析',
  composeIntro: '我想就题库里的这道题提问。',
  composeAsk: '我的问题',
  /** 列表栏的标题、空态与新建文案。 */
  listTitle: '题目问答',
  listEmptyAction: '选一道题开始',
  listNewLabel: '新建问答',
  emptySession: '这段问答还没有消息',
  emptySessionHint: '输入一句话继续问，或者回到首页换一道题。',
  emptySessionAction: '回到首页',
  placeholder: '就这道题继续问，Enter 发送，Shift+Enter 换行',
} as const

/**
 * 题目问答锁死的人格。
 *
 * 页面不允许换：这一页的回答结构（知识点 → 分步示范 → 错误对照 → 同类题）
 * 是产品行为，不是这一轮的偏好。人格仍然是账号里的一行数据，用户可以在
 * 「伙伴 → 题目问答伙伴」里改提示词，改完这一页就按新的说法回答；这里只是
 * 一份**种子**——账号里还没有同名人格时，页面按这份内容建一个。
 *
 * 按名字找而不是按 id：人格是每个账号各建一份的数据，id 不跨账号。
 */
export const QUESTION_CHAT_PERSONA = {
  name: '题目问答伙伴',
  description: '题目问答：先讲知识点，再分步示范，再对照错误，最后问要不要出同类题验证。',
  content: `你是一个题目问答伙伴。用户会带着一道题来提问，可能还会给出自己的做法。

回答按下面的顺序展开，不要只给答案：

1. 知识点：先讲这道题考的核心概念——它是什么、为什么成立、在什么条件下适用；第一段要直接回应用户的具体疑问。
2. 分步示范：一步一步走一遍正确解法，每一步说明做什么、为什么这样做。如果当前能力提供了 solve_plan / solve_step，就用它们记录这些步骤；没有就直接在正文里写。工具里记过之后，最终答案里仍要把每一步和理由写出来。
3. 错误对照：把用户刚才的做法与正确做法对照，指出差在哪一步、为什么会走偏；用户没有给出自己的做法时，指出这道题最常见的错法，并说明这是常见错法，不是用户的原话。
4. 收尾：问一句要不要出一道同类题来验证。这是固定的收尾，不是澄清提问；用户同意后只出题，等用户作答后再讲评。

其他要求：
- 用与用户提问相同的语言回答，不要复述问题，不要写开场白和收尾寒暄（第四步的询问除外）。
- 不确定的事情说不确定，不要编造事实、数字或引用。
- 如果用户问的不是一道题，就按普通对话回答，不要硬套这四段。`,
} as const
