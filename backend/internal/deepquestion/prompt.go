package deepquestion

import (
	"fmt"
	"strings"
)

// generationPrompt is what the model is told when it writes the questions.
//
// It is built per turn rather than held as one constant, because the set of
// questions is the turn's: the count, the kinds and the material come out of the
// plan ideation settled, and a constant prompt would be a second place those
// facts are written down — the one that goes stale.
//
// Two things in it are load-bearing beyond tone. The first is that a question
// leaves through save_question and not through the answer: a model that wrote
// its questions into its reply would produce a turn where nothing is stored and
// the user reads a list they cannot practise. The second is that the explanation
// is what the question is judged by — a short answer is graded against it — so a
// prompt that let the model treat it as a courtesy would produce questions that
// cannot be answered at all.
func generationPrompt(plan Plan) string {
	var prompt strings.Builder
	prompt.WriteString(`你是一位出题老师。这一轮的任务是把题写进题库，用户会一道一道地看到它们出现。

每道题单独调用一次 save_question，一次一道。题目存进去就出现在用户面前了，所以正文里不要再复述题目内容。
题目要能被判：explanation 必须说清楚为什么是这个答案——主观题就是照它判分的，用户作答之后也要靠它看懂。
题干要自足：只读题干和选项就能作答，不要写"根据材料第三段"。
出完最后一道题之后，用一两句话收尾：覆盖了什么、哪一块还没有覆盖。不要再列一遍题目。

这一轮的题组：`)
	fmt.Fprintf(&prompt, "\n- 数量：%d 道，一道都不能少，也不要多出。", plan.Count)

	if len(plan.Types) > 0 {
		names := make([]string, 0, len(plan.Types))
		for _, kind := range plan.Types {
			names = append(names, kindName(kind))
		}
		fmt.Fprintf(&prompt, "\n- 题型：只出%s。", strings.Join(names, "、"))
	} else {
		prompt.WriteString("\n- 题型：用户没有指定，三种题型混着出，以材料能支持的形式为准。")
	}
	if plan.Difficulty != "" {
		fmt.Fprintf(&prompt, "\n- 难度：%s。", difficultyName(plan.Difficulty))
	}
	if len(plan.Categories) > 0 {
		fmt.Fprintf(&prompt, "\n- 分类：出完每道题后，在 save_question 的 categories 里写上「%s」。",
			strings.Join(plan.Categories, "、"))
	}
	if plan.MaterialTitle != "" {
		fmt.Fprintf(&prompt, "\n- 取材：题目要出自本轮携带的材料，先读细再出，不要出材料里没有的事实题。"+
			"如果这道题出自某一份附件，就在 save_question 里用 source_label 写上那份附件的编号（与 read_source 的编号一致）。")
	} else {
		prompt.WriteString("\n- 取材：本轮没有携带材料，出通用题目。")
	}
	return prompt.String()
}
