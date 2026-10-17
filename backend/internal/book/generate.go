package book

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"backend/internal/llm"
)

// stripFences removes the code fence a model may have wrapped its answer in.
//
// It is copied from internal/reading/extension.go on purpose rather than
// exported from there: the book package and the reading package have no other
// reason to know each other, and a shared "model output habits" package for two
// functions and two call sites would be a layer that costs more than it saves.
func stripFences(content string) string {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	if index := strings.IndexByte(trimmed, '\n'); index >= 0 {
		trimmed = trimmed[index+1:]
	}
	if index := strings.LastIndex(trimmed, "```"); index >= 0 {
		trimmed = trimmed[:index]
	}
	return strings.TrimSpace(trimmed)
}

// decodeModelJSON reads the JSON a model produced.
//
// Models wrap JSON in a fence or introduce it with a sentence often enough that
// refusing those answers would throw away work over a formatting habit. What is
// taken is the outermost object or array and nothing else; a reply with no JSON
// at all is an error rather than a guess at what was meant.
func decodeModelJSON(content string, target any) error {
	trimmed := stripFences(content)
	start := strings.IndexAny(trimmed, "[{")
	if start < 0 {
		return fmt.Errorf("模型回答里没有 JSON")
	}
	opener := trimmed[start]
	closer := byte('}')
	if opener == '[' {
		closer = ']'
	}
	end := strings.LastIndexByte(trimmed, closer)
	if end < start {
		return fmt.Errorf("模型回答里的 JSON 不完整")
	}
	if err := json.Unmarshal([]byte(trimmed[start:end+1]), target); err != nil {
		return fmt.Errorf("模型回答不是可读的 JSON：%v", err)
	}
	return nil
}

// parseOutlineAnswer decodes and validates one model answer.
func parseOutlineAnswer(answer string) ([]OutlineItemInput, error) {
	var payload struct {
		Chapters []OutlineItemInput `json:"chapters"`
	}
	if err := decodeModelJSON(answer, &payload); err != nil {
		return nil, err
	}
	return validateOutline(payload.Chapters)
}

// validateOutline applies the shape rules to an outline, from the model or from
// the reader.
//
// It returns a cleaned copy: titles and summaries are trimmed, and the order of
// the slice is the chapter order. Rejecting rather than repairing a bad outline
// is deliberate — a chapter title is what the reader will read the book by, and
// silently shortening one changes the book rather than reporting a problem.
func validateOutline(items []OutlineItemInput) ([]OutlineItemInput, error) {
	if len(items) < MinOutlineChapters || len(items) > MaxOutlineChapters {
		return nil, fmt.Errorf("%w: 需要 %d 到 %d 章，这次是 %d 章", ErrInvalidOutline, MinOutlineChapters, MaxOutlineChapters, len(items))
	}
	cleaned := make([]OutlineItemInput, 0, len(items))
	seen := make(map[string]int, len(items))
	for index, item := range items {
		title := strings.TrimSpace(item.Title)
		summary := strings.TrimSpace(item.Summary)
		if title == "" {
			return nil, fmt.Errorf("%w: 第 %d 章没有标题", ErrInvalidOutline, index+1)
		}
		if titleRunes := []rune(title); len(titleRunes) > MaxOutlineTitleRunes {
			return nil, fmt.Errorf("%w: 第 %d 章的标题超过 %d 个字", ErrInvalidOutline, index+1, MaxOutlineTitleRunes)
		}
		if summaryRunes := []rune(summary); len(summaryRunes) > MaxOutlineSummaryRunes {
			return nil, fmt.Errorf("%w: 第 %d 章的摘要超过 %d 个字", ErrInvalidOutline, index+1, MaxOutlineSummaryRunes)
		}
		key := strings.ToLower(title)
		if earlier, exists := seen[key]; exists {
			return nil, fmt.Errorf("%w: 第 %d 章和第 %d 章同名", ErrInvalidOutline, earlier+1, index+1)
		}
		seen[key] = index
		cleaned = append(cleaned, OutlineItemInput{Title: title, Summary: summary})
	}
	return cleaned, nil
}

// validateChapterContent applies the body rules to one generated chapter.
func validateChapterContent(content string) (string, error) {
	cleaned := stripFences(content)
	if cleaned == "" {
		return "", fmt.Errorf("%w: 模型没有写出正文", ErrGenerationFailed)
	}
	if runes := []rune(cleaned); len(runes) > MaxChapterRunes {
		return "", fmt.Errorf("%w: 正文超过 %d 个字", ErrGenerationFailed, MaxChapterRunes)
	}
	return cleaned, nil
}

// stripChapterHeading removes a chapter heading the model repeated at the top
// of the body.
//
// The prompt asks it not to, and most answers obey. The ones that do not put
// the title above a body that the page already titles, which reads as the
// chapter starting twice — so exactly that heading goes, and any other opening
// heading is left alone because it may be the chapter's first section.
func stripChapterHeading(content, title string) string {
	trimmed := strings.TrimLeft(content, " \t\r\n")
	line := trimmed
	rest := ""
	if index := strings.IndexByte(trimmed, '\n'); index >= 0 {
		line = trimmed[:index]
		rest = trimmed[index+1:]
	}
	if !strings.HasPrefix(strings.TrimSpace(line), "#") {
		return content
	}
	heading := strings.TrimLeft(strings.TrimSpace(line), "#")
	if sameChapterTitle(heading, title) {
		return strings.TrimLeft(rest, " \t\r\n")
	}
	return content
}

func sameChapterTitle(heading, title string) bool {
	normalizedHeading := normalizeHeading(heading)
	normalizedTitle := normalizeHeading(title)
	if normalizedHeading == "" || normalizedTitle == "" {
		return false
	}
	if normalizedHeading == normalizedTitle {
		return true
	}
	// "第三章 标题" normalizes to "标题"; a model that also kept a series name
	// ("第 3 章 书名：标题") still ends with the chapter title.
	return strings.HasSuffix(normalizedHeading, normalizedTitle)
}

func normalizeHeading(text string) string {
	trimmed := strings.TrimSpace(strings.Trim(strings.TrimSpace(text), "#"))
	if strings.HasPrefix(trimmed, "第") {
		if index := strings.Index(trimmed, "章"); index > 0 {
			trimmed = trimmed[index+len("章"):]
		}
	}
	trimmed = strings.TrimLeft(strings.TrimSpace(trimmed), "：:、.．-— ")
	return strings.ToLower(strings.TrimSpace(trimmed))
}

// streamText runs one completion and returns its whole answer.
//
// Streaming is the only mode the provider has; there is nothing to do with the
// fragments before the answer is complete, so they are collected here. The byte
// cap is a safety net around a runaway model, not a target.
func streamText(ctx context.Context, provider llm.Provider, request llm.Request, limit int) (string, error) {
	var reply strings.Builder
	_, err := provider.Stream(ctx, request, func(delta llm.Delta) error {
		reply.WriteString(delta.Content)
		if reply.Len() > limit {
			return fmt.Errorf("the model's answer exceeded %d bytes", limit)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return reply.String(), nil
}

// askOutline generates an outline, repairing one invalid answer.
//
// Both attempts share one deadline: the second call is the same request's
// second try, not a second request, and the HTTP write timeout covers the two
// together.
func (s *Service) askOutline(ctx context.Context, title string, kind SourceKind, sourceTitle, material string) ([]OutlineItemInput, error) {
	ctx, cancel := context.WithTimeout(ctx, generationBudget)
	defer cancel()

	request := llm.Request{
		Model:       s.model,
		Temperature: outlineTemperature,
		MaxTokens:   outlineMaxTokens,
		Messages:    outlineMessages(title, kind, sourceTitle, material),
	}
	answer, err := streamText(ctx, s.provider, request, streamByteHardLimit)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrGenerationFailed, err)
	}
	items, err := parseOutlineAnswer(answer)
	if err == nil {
		return items, nil
	}

	s.repairOnce(ctx, "outline", title, err)

	request.Messages = outlineRepairMessages(title, kind, sourceTitle, material, answer, err.Error())
	repaired, repairErr := streamText(ctx, s.provider, request, streamByteHardLimit)
	if repairErr != nil {
		return nil, fmt.Errorf("%w: %v", ErrGenerationFailed, repairErr)
	}
	items, err = parseOutlineAnswer(repaired)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrGenerationFailed, err)
	}
	return items, nil
}

// askChapter generates one chapter body.
//
// There is no repair attempt here: a chapter that came back empty or absurdly
// long is retried by the reader, and spending the second half of the deadline
// on a repair in which the chapter prompt has no JSON contract to fix would
// leave nothing to send a failure back with.
func (s *Service) askChapter(ctx context.Context, title string, outline []Chapter, target Chapter, material string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, generationBudget)
	defer cancel()

	request := llm.Request{
		Model:       s.model,
		Temperature: chapterTemperature,
		MaxTokens:   chapterMaxTokens,
		Messages:    chapterMessages(title, outline, target, material),
	}
	answer, err := streamText(ctx, s.provider, request, streamByteHardLimit)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrGenerationFailed, err)
	}
	content, err := validateChapterContent(answer)
	if err != nil {
		return "", err
	}
	return stripChapterHeading(content, target.Title), nil
}

// repairOnce notes an invalid model answer. It is a log line rather than an
// error path because the repair that follows is what decides the outcome, and a
// success after a repair should leave a trace of why the second call happened.
func (s *Service) repairOnce(ctx context.Context, kind, title string, cause error) {
	if s == nil || s.logger == nil {
		return
	}
	s.logger.WarnContext(ctx, "book model answer was invalid; asking once more",
		"kind", kind, "title", title, "cause", cause)
}
