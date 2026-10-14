package tool

import (
	"encoding/json"
	"fmt"
	"strings"

	"backend/internal/mastery"
	"backend/internal/notebook"
)

// Reading the mastery tools' arguments.
//
// Every refusal in this file is written the way the source tools' refusals are
// (§Refusal): a sentence naming what was wrong with the call and how to ask
// again. None of them is about the path — a coordinate that is not in the tree,
// a lease somebody else holds, a version that moved — because those are facts
// about the world that only the runtime can look up, and a tool that guessed at
// them would refuse calls the path would have accepted.
//
// The two absences that are not refusals are worth naming. A field the model did
// not send is a field it does not want changed, which is why the edit's fields
// are pointers; and a coordinate is never trimmed into shape, because an id that
// needs trimming is an id the model copied wrong.

// parseMasteryQuestion reads a question and the knowledge point it is about.
//
// The coordinate is checked before the question's own fields, so a call that got
// both wrong is told about the coordinate first: it is the half the model has to
// go and look up, and the other half it can fix from what it already wrote.
func parseMasteryQuestion(arguments json.RawMessage, carried []Carried) (notebook.Question, string, error) {
	args, err := readQuestionArguments(arguments)
	if err != nil {
		return notebook.Question{}, "", err
	}
	point := strings.TrimSpace(args.KnowledgePointID)
	if point == "" {
		return notebook.Question{}, "", &Refusal{Note: "knowledge_point_id 是必填的：" +
			"写这道题考的那条知识点的 id（mastery_state 里 kp_ 开头的那一列），不是它的名字。"}
	}
	question, err := questionFrom(args, carried)
	if err != nil {
		return notebook.Question{}, "", err
	}
	return question, point, nil
}

// gradeRequest is one judgement's two arguments.
type gradeRequest struct {
	QuestionID string
	UserAnswer string
}

// parseGrade reads the question and the answer.
//
// A blank answer is refused rather than judged as wrong, and the refusal says
// where the blank case belongs. "The learner wrote nothing" is a real thing that
// happens, but it is an observation about their knowledge rather than an answer
// to a question, and the tool that records observations is mastery_assess. Left
// to itself the model would send an empty string for a question the user never
// answered, and the review curve would count a miss against it.
func parseGrade(arguments json.RawMessage) (gradeRequest, error) {
	var args struct {
		QuestionID string `json:"question_id"`
		UserAnswer string `json:"user_answer"`
	}
	if err := json.Unmarshal(arguments, &args); err != nil {
		return gradeRequest{}, fmt.Errorf("tool: mastery_grade arguments are not a JSON object: %w", err)
	}
	questionID := strings.TrimSpace(args.QuestionID)
	if questionID == "" {
		return gradeRequest{}, &Refusal{Note: "question_id 是必填的：它是题库里那道题的 id，" +
			"出题的时候返回过，也可以从题库里查。"}
	}
	if strings.TrimSpace(args.UserAnswer) == "" {
		return gradeRequest{}, &Refusal{Note: "user_answer 是空的。" +
			"判卷要判的是一份作答；用户确实什么都没写的话，用 mastery_assess 记一次 incorrect，" +
			"不要把空字符串交给判卷。"}
	}
	return gradeRequest{QuestionID: questionID, UserAnswer: args.UserAnswer}, nil
}

// parseObservation reads one observation.
func parseObservation(arguments json.RawMessage) (Observation, error) {
	var args struct {
		KnowledgePointID string `json:"knowledge_point_id"`
		Result           string `json:"result"`
		Detail           string `json:"detail"`
	}
	if err := json.Unmarshal(arguments, &args); err != nil {
		return Observation{}, fmt.Errorf("tool: mastery_assess arguments are not a JSON object: %w", err)
	}
	point := strings.TrimSpace(args.KnowledgePointID)
	if point == "" {
		return Observation{}, masteryRefusal("knowledge_point_id")
	}
	result := mastery.Result(strings.TrimSpace(args.Result))
	if !result.Valid() {
		return Observation{}, &Refusal{Note: fmt.Sprintf(
			"result 只能是 correct、incorrect、partial 之一，收到的是「%s」。", args.Result)}
	}
	return Observation{
		KnowledgePointID: point,
		Result:           result,
		Detail:           strings.TrimSpace(args.Detail),
	}, nil
}

// skipRequest is one skip's two arguments.
type skipRequest struct {
	KnowledgePointID string
	Reason           string
}

// parseSkip reads the knowledge point and, if there is one, the reason.
func parseSkip(arguments json.RawMessage) (skipRequest, error) {
	var args struct {
		KnowledgePointID string `json:"knowledge_point_id"`
		Reason           string `json:"reason"`
	}
	if err := json.Unmarshal(arguments, &args); err != nil {
		return skipRequest{}, fmt.Errorf("tool: mastery_skip arguments are not a JSON object: %w", err)
	}
	point := strings.TrimSpace(args.KnowledgePointID)
	if point == "" {
		return skipRequest{}, masteryRefusal("knowledge_point_id")
	}
	return skipRequest{KnowledgePointID: point, Reason: strings.TrimSpace(args.Reason)}, nil
}

// parsePathID reads the one argument mastery_switch takes.
func parsePathID(arguments json.RawMessage) (string, error) {
	var args struct {
		PathID string `json:"path_id"`
	}
	if err := json.Unmarshal(arguments, &args); err != nil {
		return "", fmt.Errorf("tool: mastery_switch arguments are not a JSON object: %w", err)
	}
	pathID := strings.TrimSpace(args.PathID)
	if pathID == "" {
		return "", &Refusal{Note: "path_id 是必填的：写要换过去的那条路径的 id（mp_ 开头）。"}
	}
	return pathID, nil
}

// parseModules reads mastery_map's outline.
//
// A module with no knowledge points under it is accepted: an outline is often
// drawn module by module, and a rule against an empty module would make the
// model wait until it had thought of everything before it could say anything.
// An empty `modules` list is refused, because that is a call that asks for
// nothing — and the state it would write is the state that is already there.
func parseModules(arguments json.RawMessage) ([]mastery.Module, error) {
	var args struct {
		Modules []struct {
			ID              string `json:"id"`
			Name            string `json:"name"`
			Description     string `json:"description"`
			KnowledgePoints []struct {
				Name        string `json:"name"`
				Type        string `json:"type"`
				Description string `json:"description"`
			} `json:"knowledge_points"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(arguments, &args); err != nil {
		return nil, fmt.Errorf("tool: mastery_map arguments are not a JSON object: %w", err)
	}
	if len(args.Modules) == 0 {
		return nil, &Refusal{Note: "modules 是空的：至少要写一个模块。" +
			"要把知识点补到哪个模块里，就写那个模块的 id；新模块直接写名字。"}
	}

	modules := make([]mastery.Module, 0, len(args.Modules))
	for index, module := range args.Modules {
		name := strings.TrimSpace(module.Name)
		if name == "" {
			return nil, &Refusal{Note: fmt.Sprintf("第 %d 个模块没写名字。", index+1)}
		}
		points := make([]mastery.KnowledgePoint, 0, len(module.KnowledgePoints))
		for position, point := range module.KnowledgePoints {
			pointName := strings.TrimSpace(point.Name)
			if pointName == "" {
				return nil, &Refusal{Note: fmt.Sprintf(
					"模块「%s」里的第 %d 条知识点没写名字。", name, position+1)}
			}
			points = append(points, mastery.KnowledgePoint{
				Name:        pointName,
				Type:        strings.TrimSpace(point.Type),
				Description: strings.TrimSpace(point.Description),
			})
		}
		modules = append(modules, mastery.Module{
			ID:              strings.TrimSpace(module.ID),
			Name:            name,
			Description:     strings.TrimSpace(module.Description),
			KnowledgePoints: points,
		})
	}
	return modules, nil
}

// parseKnowledgePointEdit reads one edit.
//
// A field the model sent is a field it wants changed, and a field it left out is
// one it does not — which is why the three wording fields are pointers here.
// A name that came through blank is refused rather than applied: a knowledge
// point renamed to nothing is one nobody can read, and "clear the name" is not a
// thing the tree has an answer for. A blank description is applied, because
// clearing a note is a thing a learner does mean.
func parseKnowledgePointEdit(arguments json.RawMessage) (KnowledgePointEdit, error) {
	var args struct {
		KnowledgePointID string  `json:"knowledge_point_id"`
		Name             *string `json:"name"`
		Type             *string `json:"type"`
		Description      *string `json:"description"`
		ToModuleID       *string `json:"to_module_id"`
		Position         *int    `json:"position"`
	}
	if err := json.Unmarshal(arguments, &args); err != nil {
		return KnowledgePointEdit{}, fmt.Errorf("tool: mastery_edit arguments are not a JSON object: %w", err)
	}
	id := strings.TrimSpace(args.KnowledgePointID)
	if id == "" {
		return KnowledgePointEdit{}, masteryRefusal("knowledge_point_id")
	}

	edit := KnowledgePointEdit{ID: id, Position: appendPosition}
	if args.Name != nil {
		name := strings.TrimSpace(*args.Name)
		if name == "" {
			return KnowledgePointEdit{}, &Refusal{Note: "name 给的是空字符串。" +
				"知识点总得有个名字，改名字要写新的名字；不想改名字就不要给这个字段。"}
		}
		edit.Edit.Name = &name
	}
	if args.Type != nil {
		value := strings.TrimSpace(*args.Type)
		edit.Edit.Type = &value
	}
	if args.Description != nil {
		value := strings.TrimSpace(*args.Description)
		edit.Edit.Description = &value
	}
	if args.ToModuleID != nil {
		moduleID := strings.TrimSpace(*args.ToModuleID)
		if moduleID == "" {
			return KnowledgePointEdit{}, &Refusal{Note: "to_module_id 给的是空字符串：" +
				"要挪到哪个模块就写那个模块的 id（mastery_state 里 m_ 那一列）；不想挪就不要给这个字段。"}
		}
		edit.ToModule = &moduleID
		if args.Position != nil {
			edit.Position = *args.Position
		}
	}
	if edit.Edit.Empty() && edit.ToModule == nil {
		return KnowledgePointEdit{}, &Refusal{Note: "这次调用没有说要改什么：" +
			"改名字写 name，改说明写 description，挪模块写 to_module_id，至少给一个。"}
	}
	return edit, nil
}

// appendPosition is what an unstated position means: put it at the end.
//
// The zero value of an int is a position — the first slot — and a move that
// nobody asked the position of landing at the front of a module is a reordering
// the model did not intend and the learner did not ask for. So "not stated" is
// spelled out here rather than left as zero.
const appendPosition = -1
