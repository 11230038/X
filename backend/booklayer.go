package main

import (
	"context"
	"fmt"
	"strings"

	"backend/internal/authz"
	"backend/internal/book"
	"backend/internal/kb"
	"backend/internal/notebook"
	"backend/internal/session"
)

// bookSources is the four things a book can be generated from, as the book
// package reaches them.
//
// It is an adapter rather than an import in the other direction (§1.2): the
// book package names a source as a kind, an id and some text, and knows nothing
// about conversations, collections, parsed documents or questions. Every
// ownership check that matters happens in the domain being read, through the
// caller each of these methods already takes.
type bookSources struct {
	sessions  *session.Service
	materials *kb.Service
	questions *notebook.Service
}

// newBookSources builds the source loader over the domains the process already
// holds.
func newBookSources(sessions *session.Service, materials *kb.Service, questions *notebook.Service) book.SourceLoader {
	return &bookSources{sessions: sessions, materials: materials, questions: questions}
}

// maxQuestionPages bounds how much of a question bank one book snapshots.
const (
	questionPageSize = 200
	maxQuestionPages = 3
	maxKnowledgeDocs = 50
)

// Load reads one source on behalf of a caller.
func (s *bookSources) Load(ctx context.Context, kind book.SourceKind, sourceID string, caller authz.Caller) (book.Source, error) {
	switch kind {
	case book.SourceSession:
		return s.loadSession(ctx, sourceID, caller)
	case book.SourceKnowledgeBase:
		return s.loadKnowledgeBase(ctx, sourceID, caller)
	case book.SourceFile:
		return s.loadFile(ctx, sourceID, caller)
	case book.SourceQuestionBank:
		return s.loadQuestionBank(ctx, caller)
	default:
		return book.Source{}, fmt.Errorf("%w: unknown source kind %q", book.ErrInvalidRequest, kind)
	}
}

func (s *bookSources) loadSession(ctx context.Context, sessionID string, caller authz.Caller) (book.Source, error) {
	if s.sessions == nil {
		return book.Source{}, book.ErrSourceUnavailable
	}
	record, err := s.sessions.Get(ctx, sessionID, caller)
	if err != nil {
		return book.Source{}, fmt.Errorf("%w: %v", book.ErrSourceUnavailable, err)
	}
	messages, err := s.sessions.Messages(ctx, sessionID, caller)
	if err != nil {
		return book.Source{}, fmt.Errorf("%w: %v", book.ErrSourceUnavailable, err)
	}

	var builder strings.Builder
	used := 0
	truncated := false
	for _, message := range messages {
		text := strings.TrimSpace(message.Content)
		if text == "" {
			continue
		}
		label := "【用户】"
		if message.Role == session.RoleAssistant {
			label = "【助手】"
		}
		if !appendWithin(&builder, &used, label+"\n"+text+"\n\n", book.SourceBudgetRunes) {
			truncated = true
			break
		}
	}
	if strings.TrimSpace(builder.String()) == "" {
		return book.Source{}, book.ErrSourceEmpty
	}
	return book.Source{
		Kind:      book.SourceSession,
		ID:        sessionID,
		Title:     record.Title,
		Text:      builder.String(),
		Truncated: truncated,
	}, nil
}

func (s *bookSources) loadKnowledgeBase(ctx context.Context, kbID string, caller authz.Caller) (book.Source, error) {
	if s.materials == nil {
		return book.Source{}, book.ErrSourceUnavailable
	}
	base, err := s.materials.KnowledgeBase(ctx, kbID, caller)
	if err != nil {
		return book.Source{}, fmt.Errorf("%w: %v", book.ErrSourceUnavailable, err)
	}
	documents, err := s.materials.Documents(ctx, kbID, caller)
	if err != nil {
		return book.Source{}, fmt.Errorf("%w: %v", book.ErrSourceUnavailable, err)
	}

	var builder strings.Builder
	used := 0
	truncated := len(documents) > maxKnowledgeDocs
	read := 0
	for _, document := range documents {
		if read >= maxKnowledgeDocs || used >= book.SourceBudgetRunes {
			truncated = true
			break
		}
		window, err := s.materials.ReadWindow(ctx, document.ID, 0, int64(book.SourceBudgetRunes-used), caller)
		if err != nil {
			// One unreadable document does not fail the source: a book from the
			// rest of the base is still a book. If nothing at all can be read the
			// empty check below answers with the state that says so.
			continue
		}
		text := strings.TrimSpace(window.Text)
		if text == "" {
			continue
		}
		if window.CharEnd < window.TotalChars {
			truncated = true
		}
		heading := "## " + strings.TrimSpace(document.Filename) + "\n\n"
		if strings.TrimSpace(document.Filename) == "" {
			heading = "## 未命名文档\n\n"
		}
		if !appendWithin(&builder, &used, heading+text+"\n\n", book.SourceBudgetRunes) {
			truncated = true
			break
		}
		read++
	}
	if strings.TrimSpace(builder.String()) == "" {
		return book.Source{}, book.ErrSourceEmpty
	}
	return book.Source{
		Kind:      book.SourceKnowledgeBase,
		ID:        kbID,
		Title:     base.Name,
		Text:      builder.String(),
		Truncated: truncated,
	}, nil
}

func (s *bookSources) loadFile(ctx context.Context, fileID string, caller authz.Caller) (book.Source, error) {
	if s.materials == nil {
		return book.Source{}, book.ErrSourceUnavailable
	}
	document, err := s.materials.DocumentOfFile(ctx, fileID, caller)
	if err != nil {
		// A file that exists but has not been parsed has no document, and from
		// here that is the same answer as a file that is not there: there is no
		// text to generate from.
		return book.Source{}, fmt.Errorf("%w: %v", book.ErrSourceUnavailable, err)
	}
	window, err := s.materials.ReadWindow(ctx, document.ID, 0, int64(book.SourceBudgetRunes), caller)
	if err != nil {
		return book.Source{}, fmt.Errorf("%w: %v", book.ErrSourceUnavailable, err)
	}
	text := strings.TrimSpace(window.Text)
	if text == "" {
		return book.Source{}, book.ErrSourceEmpty
	}
	return book.Source{
		Kind:      book.SourceFile,
		ID:        fileID,
		Title:     document.Filename,
		Text:      text,
		Truncated: document.Truncated || window.CharEnd < window.TotalChars,
	}, nil
}

func (s *bookSources) loadQuestionBank(ctx context.Context, caller authz.Caller) (book.Source, error) {
	if s.questions == nil {
		return book.Source{}, book.ErrSourceUnavailable
	}
	var builder strings.Builder
	used := 0
	truncated := false
	total := 0
	for page := 0; page < maxQuestionPages; page++ {
		entries, err := s.questions.List(ctx, notebook.Filter{
			Caller: caller,
			Limit:  questionPageSize,
			Offset: page * questionPageSize,
		})
		if err != nil {
			return book.Source{}, fmt.Errorf("%w: %v", book.ErrSourceUnavailable, err)
		}
		if len(entries) == 0 {
			break
		}
		for _, entry := range entries {
			if used >= book.SourceBudgetRunes {
				truncated = true
				break
			}
			total++
			if !appendWithin(&builder, &used, renderQuestion(total, entry), book.SourceBudgetRunes) {
				truncated = true
				break
			}
		}
		if truncated || len(entries) < questionPageSize {
			break
		}
		if page == maxQuestionPages-1 {
			truncated = true
		}
	}
	if strings.TrimSpace(builder.String()) == "" {
		return book.Source{}, book.ErrSourceEmpty
	}
	return book.Source{
		Kind:      book.SourceQuestionBank,
		Title:     "我的题库",
		Text:      builder.String(),
		Truncated: truncated,
	}, nil
}

// renderQuestion writes one question as source material: the question, its
// options, and what the answer and the explanation were.
func renderQuestion(index int, entry notebook.Entry) string {
	question := entry.Question
	var block strings.Builder
	fmt.Fprintf(&block, "### 第 %d 题（%s）\n%s\n", index, questionTypeLabel(question.Type), strings.TrimSpace(question.Text))
	for _, option := range question.Options {
		fmt.Fprintf(&block, "%s. %s\n", option.Key, strings.TrimSpace(option.Text))
	}
	if answer := strings.TrimSpace(question.CorrectAnswer); answer != "" {
		fmt.Fprintf(&block, "答案：%s\n", answer)
	}
	if explanation := strings.TrimSpace(question.Explanation); explanation != "" {
		fmt.Fprintf(&block, "解析：%s\n", explanation)
	}
	block.WriteString("\n")
	return block.String()
}

func questionTypeLabel(kind notebook.QuestionType) string {
	switch kind {
	case notebook.QuestionChoice:
		return "选择题"
	case notebook.QuestionFillBlank:
		return "填空题"
	case notebook.QuestionShortAnswer:
		return "简答题"
	default:
		return "题目"
	}
}

// appendWithin writes block and reports whether all of it fit.
//
// The budget is counted in runes and cut on a rune boundary, which is what keeps
// a Chinese material from ending in half a character.
func appendWithin(builder *strings.Builder, used *int, block string, budget int) bool {
	room := budget - *used
	if room <= 0 {
		return false
	}
	runes := []rune(block)
	if len(runes) > room {
		builder.WriteString(string(runes[:room]))
		*used += room
		return false
	}
	builder.WriteString(block)
	*used += len(runes)
	return true
}
