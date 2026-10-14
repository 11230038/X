package tool

import (
	"context"
	"encoding/json"
	"fmt"
)

// The two tools that change the tree: laying it out, and correcting it.
//
// They are the only mastery tools that write state_json, and both write it
// through the same version-guarded change, so both of them answer with the path
// as it now stands. A tool that answered a tree edit with "好的" would leave the
// model to invent the ids the edit produced, and the very next call it makes
// usually names one.

// MasteryMapTool adds modules and knowledge points.
type MasteryMapTool struct{}

var _ Tool = (*MasteryMapTool)(nil)

// NewMasteryMapTool returns the tool.
func NewMasteryMapTool() *MasteryMapTool { return &MasteryMapTool{} }

// Definition asks for the whole outline in one call.
//
// The shape is modules and, under each, the knowledge points — the shape the
// tree has, because a flat list of names would leave the model to say which
// belongs where in a second call and the two calls could disagree. Knowledge
// points are named and not numbered: the ids are issued by the service, so a
// model that had to invent one would be inventing a coordinate that the next
// call has to match exactly.
//
// The `id` on a module is the one optional identifier, and it is there for the
// second visit: once mastery_state has shown the ids, "add two more knowledge
// points to 导数入门" is a call that names that module, and naming it by id is the
// only way to say it that cannot match two modules.
func (t *MasteryMapTool) Definition() Definition {
	return Definition{
		Name: MasteryMapName,
		Description: "把要学的知识点按模块写进这条路径的图里。" +
			"一次调用写一整层：几个模块，每个模块下面几条知识点。" +
			"已经存在的模块要往里加知识点时，给它写 id（从 mastery_state 里取），新模块不用写。" +
			"新加的东西会拿到 id，调用返回的图里能看到——后面出题、评估都用那些 id。" +
			"只列要学的、能考的东西，不要写成章节目录。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"modules": {
					"type": "array",
					"minItems": 1,
					"description": "按先后顺序排列的模块；已经有的模块只往里补知识点，原有的不会丢",
					"items": {
						"type": "object",
						"properties": {
							"id": {
								"type": "string",
								"description": "已有模块的 id；新模块不要给这个字段"
							},
							"name": {
								"type": "string",
								"description": "模块名，如「导数入门」"
							},
							"description": {
								"type": "string",
								"description": "这个模块讲什么，一句话，可以不写"
							},
							"knowledge_points": {
								"type": "array",
								"description": "这个模块下的知识点，按先后顺序",
								"items": {
									"type": "object",
									"properties": {
										"name": {
											"type": "string",
											"description": "一条要学的东西，如「导数的定义」"
										},
										"type": {
											"type": "string",
											"description": "它的种类，如「概念」「技能」，可以不写"
										},
										"description": {
											"type": "string",
											"description": "一句话说明，可以不写"
										}
									},
									"required": ["name"],
									"additionalProperties": false
								}
							}
						},
						"required": ["name"],
						"additionalProperties": false
					}
				}
			},
			"required": ["modules"],
			"additionalProperties": false
		}`),
	}
}

// Invoke writes the outline and returns the whole tree.
//
// The whole tree and not the part that was added: the levels of the knowledge
// points already there are recomputed on every write of the state, so the tree
// that comes back is the only current reading of the path, and a diff would have
// to leave out the half of it that moved.
func (t *MasteryMapTool) Invoke(ctx context.Context, call Call) (Result, error) {
	path, err := masteryPathFrom(ctx)
	if err != nil {
		return Result{}, err
	}
	modules, err := parseModules(call.Arguments)
	if err != nil {
		return refused(err)
	}
	view, err := path.Map(ctx, modules)
	if err != nil {
		return refused(err)
	}
	return Result{
		Content:  masteryText(view),
		Success:  true,
		Metadata: masteryMetadata(view),
	}, nil
}

// MasteryEditTool rewords or moves one knowledge point.
type MasteryEditTool struct{}

var _ Tool = (*MasteryEditTool)(nil)

// NewMasteryEditTool returns the tool.
func NewMasteryEditTool() *MasteryEditTool { return &MasteryEditTool{} }

// Definition asks only for what is being changed.
//
// Every field but the coordinate is optional and an absent one means "leave it
// alone", which is why the description says that rather than leaving the model
// to send empty strings: a name sent as "" would rename the knowledge point to
// nothing, and a tree with five nameless entries in it is one nobody can read.
func (t *MasteryEditTool) Definition() Definition {
	return Definition{
		Name: MasteryEditName,
		Description: "改一条知识点：换个说法、挪到别的模块，或者两样一起改。" +
			"只给要改的字段，没给的字段保持原样——不要用空字符串来清空它们。" +
			"挪到别的模块要用 to_module_id 指那个模块的 id，且那个模块得已经在图里。" +
			"改措辞和挪位置都算改图，都会记进历史。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"knowledge_point_id": {
					"type": "string",
					"description": "要改的知识点 id，从 mastery_state 里取（kp_ 开头）"
				},
				"name": {
					"type": "string",
					"description": "新的名字；不改名字就不要给"
				},
				"type": {
					"type": "string",
					"description": "新的种类；不改就不要给"
				},
				"description": {
					"type": "string",
					"description": "新的说明；不改就不要给"
				},
				"to_module_id": {
					"type": "string",
					"description": "要挪到哪个模块（写模块 id）；不挪就不要给"
				},
				"position": {
					"type": "integer",
					"description": "挪过去之后排第几个（从 0 数）；不给或给负数表示放到最后"
				}
			},
			"required": ["knowledge_point_id"],
			"additionalProperties": false
		}`),
	}
}

// Invoke applies the edit and says where the knowledge point ended up.
func (t *MasteryEditTool) Invoke(ctx context.Context, call Call) (Result, error) {
	path, err := masteryPathFrom(ctx)
	if err != nil {
		return Result{}, err
	}
	edit, err := parseKnowledgePointEdit(call.Arguments)
	if err != nil {
		return refused(err)
	}
	view, err := path.Edit(ctx, edit)
	if err != nil {
		return refused(err)
	}

	content := fmt.Sprintf("已改好：%s。", pointText(view, edit.ID))
	if edit.ToModule != nil {
		// Where it landed is the one thing about a move that the level does not
		// already say, and the model is usually about to write another edit that
		// depends on it.
		if module, ok := view.Path.Tree.Module(*edit.ToModule); ok {
			content = fmt.Sprintf("已改好：%s，现在在模块「%s」里。", pointText(view, edit.ID), module.Name)
		}
	}
	return Result{
		Content:  content,
		Success:  true,
		Metadata: masteryMetadata(view),
	}, nil
}
