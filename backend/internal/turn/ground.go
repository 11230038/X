package turn

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"backend/internal/capability"
	"backend/internal/kb"
	"backend/internal/llm"
	"backend/internal/tool"
)

// ground runs this turn's pre-survey: the question is asked of everything the
// turn carries, and what comes back is folded into one message for the model
// (CHAT-09).
//
// No model is called here. The survey's product is material turned into a
// numbered, located list an answer can lean on, and having a model restate that
// list would pay twice for the same characters to make it read better.
//
// It fails only when the turn itself does. A search this service could not run
// is reported as material that could not be read and the turn goes on — the rule
// an attachment that failed to parse already follows, for the same reason:
// losing the survey costs recall and not correctness (§2.3).
func (r *turnRun) ground(ctx context.Context, query string) (capability.Grounding, error) {
	query = strings.TrimSpace(query)
	grounding := capability.Grounding{}
	surveyed := false

	// The reader's selection comes first, and it is the one item of the survey
	// that is not a search: it is the passage they were looking at when they
	// asked, so it is what the answer is about rather than something the answer
	// might be found in (§2.8). It is numbered with everything else, at the end —
	// it is [1] because it was handed over first, and the model cites it the same
	// way it cites a chunk. Numbering it here as well would spend the turn's count
	// twice on one passage and leave it numbered 2.
	if r.material.Selection != nil {
		grounding.Sources = append(grounding.Sources, *r.material.Selection)
		surveyed = true
	}
	if r.material.SelectionNote != "" {
		grounding.Unavailable = append(grounding.Unavailable, r.material.SelectionNote)
	}

	if len(r.material.Attachments) == 0 && len(r.material.KnowledgeBases) == 0 {
		// A turn carrying nothing has nothing to survey, so it gets a zero
		// Grounding rather than a message saying so. This is what keeps such a
		// turn's stream identical to the one V1 produced — and it is still what
		// a turn carrying nothing gets, whether or not it carried a selection:
		// the branch below is the same one, reached for the same reason.
		if !surveyed && len(grounding.Unavailable) == 0 {
			return capability.Grounding{}, nil
		}
		// A selection is the whole of this survey, and it still takes its number
		// from the turn: the read a tool makes afterwards is the next passage, not
		// a second [1].
		grounding.Sources = r.number(grounding.Sources)
		grounding.Message = llm.Message{
			Role:    llm.RoleSystem,
			Content: surveyText(r.material, grounding, surveyed),
		}
		r.unavailable = len(grounding.Unavailable)
		return grounding, nil
	}

	for _, attachment := range r.material.Attachments {
		if attachment.Unavailable != "" {
			// The reason is already a sentence a reader can act on (ATT-04), and
			// it is the same one the tool path hands back.
			grounding.Unavailable = append(grounding.Unavailable, unreadable(attachment))
			continue
		}
		if query == "" {
			continue
		}
		sources, err := r.runner.materials.Locate(ctx, attachment.DocumentID, query, 0, r.caller)
		if err != nil {
			notes, err := r.surveyNotes(ctx, err, "附件「"+attachment.Label+"」")
			if err != nil {
				return capability.Grounding{}, err
			}
			grounding.Unavailable = append(grounding.Unavailable, notes...)
			continue
		}
		surveyed = true
		grounding.Sources = append(grounding.Sources, sources...)
	}

	if len(r.material.KnowledgeBases) > 0 && query != "" {
		retrieval, err := r.runner.materials.Retrieve(ctx, kb.RetrieveInput{
			KBIDs: r.material.KnowledgeBases, Caller: r.caller,
			Query: query,
		})
		if err != nil {
			notes, err := r.surveyNotes(ctx, err, "本轮的知识库")
			if err != nil {
				return capability.Grounding{}, err
			}
			grounding.Unavailable = append(grounding.Unavailable, notes...)
		} else {
			surveyed = true
			grounding.Sources = append(grounding.Sources, retrieval.Sources...)
			for _, kbID := range retrieval.Unavailable {
				grounding.Unavailable = append(grounding.Unavailable, goneKB(kbID))
			}
		}
	}

	// The numbering is the turn's, taken here as a whole list rather than per
	// read: a source the survey found and a source a read finds afterwards share
	// one count, so a number in the answer means one passage (AIQ-02).
	grounding.Sources = r.number(grounding.Sources)
	grounding.Message = llm.Message{
		Role:    llm.RoleSystem,
		Content: surveyText(r.material, grounding, surveyed),
	}
	// What the result event reports as unreadable material is exactly the list
	// the model was given, counted once.
	r.unavailable = len(grounding.Unavailable)
	return grounding, nil
}

// surveyNotes says what to tell the model about a search that did not run.
//
// A question the index cannot answer is the model's to fix, so it is given the
// same sentence the source tools refuse with rather than a second wording of it.
// Anything else is this service's own trouble: it is reported as a gap in the
// same shape as every other gap, because a model handed a storage failure would
// repeat it as though it were a fact about the material. The error itself goes
// to the log, where a person can act on it.
//
// The turn's own cancellation is not a gap and is returned: a survey that was
// stopped because the turn was stopped must not be turned into content the
// capability then sends to a model.
func (r *turnRun) surveyNotes(ctx context.Context, err error, what string) ([]string, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var refusal *tool.Refusal
	if errors.As(explainRefusal(err, what), &refusal) {
		return []string{refusal.Note}, nil
	}
	r.runner.logger.WarnContext(ctx, "a search the pre-survey ran failed",
		"turn_id", r.turn.ID, "what", what, "error", err)
	return []string{what + "这次没能查到内容"}, nil
}

// surveyText folds the survey into the one message the model is given.
//
// It has three parts and they answer three different questions: what this turn
// carries, which is also where the numbers a model may read by come from; what
// the survey found, numbered the way an answer cites it; and what could not be
// read at all, which the model has to be able to say out loud rather than
// mistake for material that had nothing to offer (ATT-04, KB-07).
func surveyText(material Material, grounding capability.Grounding, surveyed bool) string {
	var text strings.Builder
	text.WriteString("本轮携带的材料：\n")
	if material.Selection != nil {
		// The passage the reader had chosen is named here as well as appearing
		// numbered below, because the two say different things: this says why it
		// is there at all — it is what the reader is looking at — and the list
		// below is where a model reads its number from.
		text.WriteString("- 读者划出的一段原文（就是下面编号最小的那一条）\n")
	}
	for _, attachment := range material.Attachments {
		fmt.Fprintf(&text, "- 附件 %s", attachment.Label)
		if attachment.Filename != "" {
			fmt.Fprintf(&text, "《%s》", attachment.Filename)
		}
		text.WriteString("\n")
	}
	if len(material.KnowledgeBases) > 0 {
		fmt.Fprintf(&text, "- 知识库 %d 个，可以检索\n", len(material.KnowledgeBases))
	}

	if surveyed {
		text.WriteString("\n以下是本轮材料里与当前问题相关的部分")
		text.WriteString("（回答时用方括号里的编号指向它们；材料里没有依据的，明说没有找到，不要编造）：\n")
		if len(grounding.Sources) > 0 {
			text.WriteString(tool.SourcesText(grounding.Sources))
		} else {
			text.WriteString("没有找到相关的片段。\n")
		}
	}

	if len(grounding.Unavailable) > 0 {
		text.WriteString("\n以下材料没有读到：\n")
		for _, note := range grounding.Unavailable {
			text.WriteString("- ")
			text.WriteString(note)
			text.WriteString("\n")
		}
	}

	// The closing line delimits the block: everything above it is the service's
	// account of the material, and a model that cannot see where that account
	// ends has no way to tell it from what the user said.
	if surveyed {
		text.WriteString("\n以上是本轮携带的材料里与问题相关的部分。")
		return text.String()
	}
	return strings.TrimRight(text.String(), "\n")
}
