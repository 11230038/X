package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ReadSourceName is the name read_source mounts and is called under.
const ReadSourceName = "read_source"

// ErrMissingLabel reports a read_source call that did not say which attachment.
var ErrMissingLabel = errors.New("tool: read_source needs a label")

// ReadSourceTool reads one of the attachments this turn carries.
//
// It is mounted only for a turn that carries attachments, and the label it takes
// is the turn's own numbering: there is no argument by which a model could name
// a file the turn was not given (KB-07).
type ReadSourceTool struct {
	sources Sources
}

var _ Tool = (*ReadSourceTool)(nil)

// NewReadSourceTool returns the tool bound to a turn runtime.
func NewReadSourceTool(sources Sources) *ReadSourceTool {
	return &ReadSourceTool{sources: sources}
}

// Definition declares the label as required. The description is written for the
// model, so it explains where the numbers come from rather than how the tool
// works inside.
func (t *ReadSourceTool) Definition() Definition {
	return Definition{
		Name: ReadSourceName,
		Description: "读取本轮携带的某个附件。label 是该附件在本轮材料清单里的编号（从 1 开始）。" +
			"不给 query 时按窗口读它的正文（可用 offset / limit 指定范围）；" +
			"给 query 时在这个附件内部定位与问题最相关的片段。" +
			"只能读本轮携带的附件，读不到本轮之外的任何文件。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"label": {
					"type": "string",
					"description": "附件在本轮材料清单里的编号，例如 \"1\""
				},
				"query": {
					"type": "string",
					"description": "要在该附件内部定位什么内容；给了它就不按窗口读"
				},
				"offset": {
					"type": "integer",
					"description": "从该附件的第几个字符开始读，省略表示从头开始"
				},
				"limit": {
					"type": "integer",
					"description": "最多读多少个字符，省略表示由服务端决定"
				}
			},
			"required": ["label"],
			"additionalProperties": false
		}`),
	}
}

// Invoke reads the attachment and returns what it found.
func (t *ReadSourceTool) Invoke(ctx context.Context, call Call) (Result, error) {
	var args struct {
		// Label is read as raw JSON because the numbering is the one place a
		// model's own idea of a number shows: it may send the number or the
		// string, and both name the same attachment.
		Label  json.RawMessage `json:"label"`
		Query  string          `json:"query"`
		Offset int64           `json:"offset"`
		Limit  int64           `json:"limit"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		return Result{}, fmt.Errorf("tool: read_source arguments are not a JSON object: %w", err)
	}
	label, err := labelOf(args.Label)
	if err != nil {
		return Result{}, err
	}

	reading, err := t.sources.ReadSource(ctx, ReadRequest{
		Label:  label,
		Query:  strings.TrimSpace(args.Query),
		Offset: args.Offset,
		Limit:  args.Limit,
	})
	if err != nil {
		return refused(err)
	}
	return renderReading(reading), nil
}

// labelOf reads the attachment number out of whatever the model wrote.
//
// A number and the string of that number are the same request, and failing one
// of them would cost the user a turn over a difference the model cannot see.
// Anything else is a malformed argument and stays an error, like ask_user's
// missing question: it is a mistake about the call, not about the material.
func labelOf(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", ErrMissingLabel
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if label := strings.TrimSpace(text); label != "" {
			return label, nil
		}
		return "", ErrMissingLabel
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		if label := strings.TrimSpace(number.String()); label != "" {
			return label, nil
		}
		return "", ErrMissingLabel
	}
	return "", fmt.Errorf("tool: read_source label must be a number or a string, got %s", raw)
}
