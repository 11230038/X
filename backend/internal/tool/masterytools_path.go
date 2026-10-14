package tool

import (
	"context"
	"encoding/json"
	"fmt"
)

// The three tools about the path itself: reading it, moving to another, and
// letting go of the one in hand.
//
// They are one file because they are one subject — which path this turn is
// working on — while the four that write content into a path are about what the
// path holds.

// MasteryStateTool reads the current path.
type MasteryStateTool struct{}

var _ Tool = (*MasteryStateTool)(nil)

// NewMasteryStateTool returns the tool.
func NewMasteryStateTool() *MasteryStateTool { return &MasteryStateTool{} }

// Definition takes no arguments at all.
//
// Which path a turn is on is the runtime's to decide and the model's to find
// out, so there is nothing here to name — the same shape solve_step has, and for
// the same reason: an argument that could only ever be one value is an argument
// the model can get wrong.
func (t *MasteryStateTool) Definition() Definition {
	return Definition{
		Name: MasteryStateName,
		Description: "读出当前掌握路径的状态：目标、版本，以及每个模块和每条知识点的掌握档位。" +
			"知识点 id 只在这里能看到，出题、评估、改图之前都先看一眼。" +
			"它不接受参数：这一轮在学哪条路径不是你能选的。",
	}
}

// Invoke reads the path.
func (t *MasteryStateTool) Invoke(ctx context.Context, call Call) (Result, error) {
	path, err := masteryPathFrom(ctx)
	if err != nil {
		return Result{}, err
	}
	view, err := path.Current(ctx)
	if err != nil {
		return refused(err)
	}
	return Result{
		Content:  masteryText(view),
		Success:  true,
		Metadata: masteryMetadata(view),
	}, nil
}

// MasterySwitchTool moves the turn to another path.
type MasterySwitchTool struct{}

var _ Tool = (*MasterySwitchTool)(nil)

// NewMasterySwitchTool returns the tool.
func NewMasterySwitchTool() *MasterySwitchTool { return &MasterySwitchTool{} }

// Definition asks for the path id and says what switching does and does not do.
//
// Two things are worth telling the model here. The other path is named by id and
// not by goal text, because a phrase that matched two paths would leave the
// choice to string comparison; and switching is not a loss of work — the
// observations already recorded stay with the path they were made on.
func (t *MasterySwitchTool) Definition() Definition {
	return Definition{
		Name: MasterySwitchName,
		Description: "换到另一条掌握路径上去，之后的操作都算在那条路径上。" +
			"path_id 从路径列表里取；这条路径上已经记下的题和证据都留着。" +
			"只有用户确实要学别的东西时才换。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path_id": {
					"type": "string",
					"description": "要换到的那条路径的 id"
				}
			},
			"required": ["path_id"],
			"additionalProperties": false
		}`),
	}
}

// Invoke switches and reports where the turn now stands.
//
// The new path is rendered in full rather than acknowledged, because a switch
// that answered "好的" would leave the model with no idea what it had switched
// to — and the very next thing it has to do is name a knowledge point on it.
func (t *MasterySwitchTool) Invoke(ctx context.Context, call Call) (Result, error) {
	path, err := masteryPathFrom(ctx)
	if err != nil {
		return Result{}, err
	}
	pathID, err := parsePathID(call.Arguments)
	if err != nil {
		return refused(err)
	}
	view, err := path.Switch(ctx, pathID)
	if err != nil {
		return refused(err)
	}
	return Result{
		Content:  masteryText(view),
		Success:  true,
		Metadata: masteryMetadata(view),
	}, nil
}

// MasteryLeaveTool releases the turn's claim on the path.
type MasteryLeaveTool struct{}

var _ Tool = (*MasteryLeaveTool)(nil)

// NewMasteryLeaveTool returns the tool.
func NewMasteryLeaveTool() *MasteryLeaveTool { return &MasteryLeaveTool{} }

// Definition says what leaving is and, more usefully, what it is not.
//
// The model's likely mistake is to read "leave" as "discard": a model that
// thought it was throwing the path away would avoid calling it, and the lease
// would sit there until it expired. So the description says plainly that the
// work stays and only the claim goes.
func (t *MasteryLeaveTool) Definition() Definition {
	return Definition{
		Name: MasteryLeaveName,
		Description: "离开这条掌握路径，把写着的那把锁交出去，别的 turn 就能接着写它。" +
			"学到的东西一样都不会少：树、证据和题都留着。" +
			"这一轮的路径活儿干完了就调用它，不要留着一把不写的锁。",
	}
}

// Invoke releases the claim. It reports no path id, because the sentence is
// about what the turn gave up rather than about which row it gave it up on — and
// the id was in the state the turn read at the start.
func (t *MasteryLeaveTool) Invoke(ctx context.Context, call Call) (Result, error) {
	path, err := masteryPathFrom(ctx)
	if err != nil {
		return Result{}, err
	}
	if err := path.Leave(ctx); err != nil {
		return refused(err)
	}
	return Result{
		Content: "已离开这条掌握路径，锁已交出；路径上的内容一条都没少。" +
			"要继续做的话，下一轮重新进入这条路径就行。",
		Success: true,
	}, nil
}

// masteryRefusal renders a refusal for the tools whose only argument is a
// coordinate, so that the three of them refuse a blank id the same way.
func masteryRefusal(field string) error {
	return &Refusal{Note: fmt.Sprintf(
		"%s 是必填的，而且不能为空：它是 mastery_state 里那串 id（如 kp_ 开头的那一列）。",
		field)}
}
