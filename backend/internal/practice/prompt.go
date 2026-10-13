package practice

import (
	"strings"

	"backend/internal/notebook"
)

// judgeSystemPrompt is what the judge is told it is.
//
// It is model-facing text, so it is written in the language the product's users
// write in, and it lives in code rather than in configuration — the same choice
// every other prompt in this service makes.
//
// Three of its instructions carry weight beyond politeness:
//
//   - the explanation is the whole basis. A judge that consults its own
//     knowledge marks an answer wrong for not matching the answer the question
//     actually has, which is the one failure a user cannot argue with.
//   - the reply is one JSON object and nothing else. The parser refuses
//     anything else on purpose, so a sentence of introduction costs a
//     judgement, and the prompt says so in as many words.
//   - "I cannot judge this" is a verdict of false with a reason. The shape has
//     no third state to put it in, which is what keeps a failure from ever
//     being read as a pass.
const judgeSystemPrompt = `你批改一道主观题的作答，判断它算不算对。

判分只依据我给你的「参考答案与解析」，不要依据你自己的知识补充标准答案：解析就是这道题的采分依据，与解析不一致的作答即使在你看来更合理，也按解析判。解析里没提到的，不要当成采分点。

只输出一个 JSON 对象，不要代码块围栏，不要在它前后写任何别的文字。字段如下：

- is_correct：布尔，这份作答算不算对。覆盖了解析里的采分点才算对；方向对但关键处错、只沾到边，都算不对。
- reason：为什么这么判。要指到题干和作答里的具体片段，不要写"基本正确"这类没有内容的评语。
- rubric：你实际按哪几点判的清单，每条一句，来自解析里的采分点。
- feedback：给用户的评语，说清哪些答对了、哪些缺了或错了。
- suggestion：下次怎么改进，具体到可以照着做。
- confidence：0 到 1 的数，你对这次判断的信心。
- boundary：这次判断判不了什么。例如"这题没有唯一标准答案，我只按你给的解析判""作答太短，看不出是否掌握"。

判不了的时候不要猜：如果这份作答无法依据解析判断（答非所问、只写了几句看不出意图、解析本身不足以判断），把 is_correct 设为 false，并在 reason 和 boundary 里说清楚为什么判不了。把判不了当成答对是不可接受的。`

// judgeQuestion is the whole of what the judge is shown: the question, the
// basis it must judge against, and the answer.
//
// The labels are what make the parts data rather than prose. An answer is free
// text and the model reads it as such; being told where it begins and where the
// explanation ended is what keeps an answer that reads like an instruction an
// answer.
//
// An empty explanation is passed on as an empty explanation rather than
// refused. The judge cannot invent the basis, but it can say in its boundary
// that there was none, and a verdict that names its own missing basis is more
// use than an error that says nothing about the question.
func judgeQuestion(question notebook.Question, userAnswer string) string {
	var builder strings.Builder
	builder.WriteString("题干：\n")
	builder.WriteString(question.Text)
	builder.WriteString("\n\n参考答案与解析（判分只依据这一段）：\n")
	if strings.TrimSpace(question.Explanation) == "" {
		builder.WriteString("（这道题没有给出解析）\n")
	} else {
		builder.WriteString(question.Explanation)
		builder.WriteString("\n")
	}
	builder.WriteString("\n用户的作答：\n")
	if strings.TrimSpace(userAnswer) == "" {
		builder.WriteString("（用户没有作答）")
	} else {
		builder.WriteString(userAnswer)
	}
	return builder.String()
}
