package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// The two tools that say something about one knowledge point without touching
// the tree: what was seen, and what was passed over.
//
// They are apart from the question pair because they are apart in the domain.
// Asking a question and judging it is one transaction with the learner — the
// question is in the bank, the answer is on the review curve — while these two
// write a single line about a coordinate and are done.

// MasteryAssessTool records one observation.
type MasteryAssessTool struct{}

var _ Tool = (*MasteryAssessTool)(nil)

// NewMasteryAssessTool returns the tool.
func NewMasteryAssessTool() *MasteryAssessTool { return &MasteryAssessTool{} }

// Definition asks for the coordinate, the outcome and the reason, and it says
// what the outcome is *for*.
//
// The description carries the gate's shape in one sentence on purpose (§2.4).
// A model that thought a single "correct" moved a knowledge point to mastered,
// or that thought not enough evidence was the same as doing badly, would file
// observations to produce a state rather than to record one — and the state it
// produced would be a claim about the learner that nothing supports.
func (t *MasteryAssessTool) Definition() Definition {
	return Definition{
		Name: MasteryAssessName,
		Description: "给一条知识点记一次观察，这是掌握档位唯一的来源。" +
			"result 只能是 correct / incorrect / partial：连续若干次 correct 才升到「已掌握」，" +
			"证据太少的知识点停在「未学」而不是「薄弱」——所以照实记，一次观察改变不了什么。" +
			"detail 写一句为什么这么判，它会被存下来给人看。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"knowledge_point_id": {
					"type": "string",
					"description": "这条知识点的 id，从 mastery_state 里取（kp_ 开头）"
				},
				"result": {
					"type": "string",
					"enum": ["correct", "incorrect", "partial"],
					"description": "correct 答对或做到了、incorrect 答错或没做到、partial 只做到一部分"
				},
				"detail": {
					"type": "string",
					"description": "一句话说明这次观察到的是什么，可以不写"
				}
			},
			"required": ["knowledge_point_id", "result"],
			"additionalProperties": false
		}`),
	}
}

// Invoke records the observation and reports what the level became.
//
// The level is reported because it is the consequence of the call and the model
// cannot work it out: the gate counts a run over every observation the path has,
// most of which this turn never saw.
func (t *MasteryAssessTool) Invoke(ctx context.Context, call Call) (Result, error) {
	path, err := masteryPathFrom(ctx)
	if err != nil {
		return Result{}, err
	}
	observation, err := parseObservation(call.Arguments)
	if err != nil {
		return refused(err)
	}
	view, err := path.Observe(ctx, observation)
	if err != nil {
		return refused(err)
	}
	return Result{
		Content: fmt.Sprintf("已记下「%s」这次观察：%s。",
			resultText(observation.Result), pointText(view, observation.KnowledgePointID)),
		Success:  true,
		Metadata: masteryMetadata(view),
	}, nil
}

// MasterySkipTool records that the learner moved past a knowledge point.
type MasterySkipTool struct{}

var _ Tool = (*MasterySkipTool)(nil)

// NewMasterySkipTool returns the tool.
func NewMasterySkipTool() *MasterySkipTool { return &MasterySkipTool{} }

// Definition says, in as many words, that a skip is not a wrong answer.
//
// This is the one thing about this tool a model could get wrong in a way that
// costs the learner something: a skip recorded as evidence would lower a level,
// and the learner who said "这条先放着" would come back to find themselves marked
// weak at it. So the description says the level does not move, and the reason is
// asked for because "why did we pass over it" is the only part of a skip that is
// worth keeping.
func (t *MasterySkipTool) Definition() Definition {
	return Definition{
		Name: MasterySkipName,
		Description: "记下这次跳过某条知识点。" +
			"跳过不是答错：它不会记成一次观察，掌握档位一点都不会变，只是把「这轮先不学它」记下来。" +
			"reason 写一句为什么跳过，用户回头看得见。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"knowledge_point_id": {
					"type": "string",
					"description": "跳过的知识点 id，从 mastery_state 里取（kp_ 开头）"
				},
				"reason": {
					"type": "string",
					"description": "为什么跳过，一句话，可以不写"
				}
			},
			"required": ["knowledge_point_id"],
			"additionalProperties": false
		}`),
	}
}

// Invoke records the skip.
func (t *MasterySkipTool) Invoke(ctx context.Context, call Call) (Result, error) {
	path, err := masteryPathFrom(ctx)
	if err != nil {
		return Result{}, err
	}
	skip, err := parseSkip(call.Arguments)
	if err != nil {
		return refused(err)
	}
	if err := path.Skip(ctx, skip.KnowledgePointID, skip.Reason); err != nil {
		return refused(err)
	}

	content := fmt.Sprintf("已记下跳过 %s。它的掌握档位没有变。", skip.KnowledgePointID)
	if reason := strings.TrimSpace(skip.Reason); reason != "" {
		content = fmt.Sprintf("已记下跳过 %s：%s。它的掌握档位没有变。", skip.KnowledgePointID, reason)
	}
	return Result{
		Content: content,
		Success: true,
		Metadata: map[string]any{
			"knowledge_point_id": skip.KnowledgePointID,
			"skipped":            true,
		},
	}, nil
}
