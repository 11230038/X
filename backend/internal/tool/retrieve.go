package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// RetrieveName is the name retrieve mounts and is called under.
const RetrieveName = "retrieve"

// ErrMissingQuery reports a retrieve call with nothing to search for.
var ErrMissingQuery = errors.New("tool: retrieve needs a query")

// RetrieveTool searches the knowledge bases this turn carries.
//
// Like read_source it takes no scope: which collections a search may reach is
// decided by the turn, so a model that names none gets an answer about the
// collections it was given and nothing else (KB-07).
type RetrieveTool struct {
	sources Sources
}

var _ Tool = (*RetrieveTool)(nil)

// NewRetrieveTool returns the tool bound to a turn runtime.
func NewRetrieveTool(sources Sources) *RetrieveTool {
	return &RetrieveTool{sources: sources}
}

// Definition asks for the question and says what the answer is good for.
func (t *RetrieveTool) Definition() Definition {
	return Definition{
		Name: RetrieveName,
		Description: "在本轮携带的知识库里检索与问题相关的片段，返回带编号的来源。" +
			"回答里引用这些内容时，必须使用这里给出的编号，不要自己编造编号、页码或文件名。" +
			"只能检索本轮携带的知识库。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": {
					"type": "string",
					"description": "要检索的问题或关键词，写得具体一些；太短的词无法检索"
				}
			},
			"required": ["query"],
			"additionalProperties": false
		}`),
	}
}

// Invoke searches and returns what it found.
func (t *RetrieveTool) Invoke(ctx context.Context, call Call) (Result, error) {
	var args struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		return Result{}, fmt.Errorf("tool: retrieve arguments are not a JSON object: %w", err)
	}
	query := strings.TrimSpace(args.Query)
	if query == "" {
		return Result{}, ErrMissingQuery
	}

	reading, err := t.sources.Retrieve(ctx, query)
	if err != nil {
		return refused(err)
	}
	return renderReading(reading), nil
}
