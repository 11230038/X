package tool

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"backend/internal/event"
	"backend/internal/notebook"
)

// option is one choice as the model wrote it.
type option struct {
	Key  string `json:"key"`
	Text string `json:"text"`
}

// readOptions reads the choices of a question, refusing the shapes that would
// make one unreachable.
//
// A question whose options are not there, or whose option keys are not distinct
// under the fold the judge uses, is a question with an option nobody can choose:
// the judge builds its set of offered keys from folded keys, so two options that
// fold together are one option to it, and the answer can only ever name one of
// the rows.
func readOptions(given []option, questionType notebook.QuestionType) ([]notebook.Option, error) {
	if questionType != notebook.QuestionChoice {
		if len(given) > 0 {
			return nil, &Refusal{Note: fmt.Sprintf(
				"%s 题不需要 options，把 options 去掉；只有 choice 题有选项。", questionType)}
		}
		return nil, nil
	}
	if len(given) < MinOptions || len(given) > MaxOptions {
		return nil, &Refusal{Note: fmt.Sprintf(
			"选择题的 options 要 %d–%d 个，收到 %d 个。",
			MinOptions, MaxOptions, len(given))}
	}
	options := make([]notebook.Option, 0, len(given))
	seen := make(map[string]bool, len(given))
	for _, choice := range given {
		key := strings.TrimSpace(choice.Key)
		text := strings.TrimSpace(choice.Text)
		if key == "" {
			return nil, &Refusal{Note: "options 里有一项没写 key，每一项都要有 key（如 \"A\"）和 text。"}
		}
		if text == "" {
			return nil, &Refusal{Note: fmt.Sprintf(
				"选项「%s」的 text 是空的，每个选项都要写明内容。", key)}
		}
		folded := foldKey(key)
		if seen[folded] {
			return nil, &Refusal{Note: fmt.Sprintf(
				"options 里有两项的 key 是同一个（都是「%s」），选项键必须互不相同。", key)}
		}
		seen[folded] = true
		options = append(options, notebook.Option{Key: key, Text: text})
	}
	return options, nil
}

// readAnswer reads the correct answer in the form the question's type stores it,
// which is the form the judge reads it back in.
func readAnswer(answer string, questionType notebook.QuestionType, options []notebook.Option) (string, error) {
	switch questionType {
	case notebook.QuestionChoice:
		return readChoiceAnswer(answer, options)
	case notebook.QuestionFillBlank:
		alternatives := cleanParts(answer, "|")
		if len(alternatives) == 0 {
			return "", &Refusal{Note: "correct_answer 是空的。" +
				"填空题写答案文本，有几个可接受的写法就写成 \"2 年|两年\"（用 | 分隔）。"}
		}
		// Stored with the alternatives joined back up, one spelling each and no
		// empties: the judge reads this column by splitting it on "|", so what is
		// written here is the whole of what it will accept.
		return strings.Join(alternatives, "|"), nil
	default:
		trimmed := strings.TrimSpace(answer)
		if trimmed == "" {
			return "", &Refusal{Note: "correct_answer 是空的，主观题要写参考答案。"}
		}
		return trimmed, nil
	}
}

// readChoiceAnswer reads a choice question's answer as the option keys it names,
// spelled the way the options spell them, each of them once, in ascending order.
//
// The order is the storage convention (notebook.Question.CorrectAnswer), and
// sorting here is what keeps the same answer written two ways one stored value:
// without it, "A,C" and "C,A" would be two answers to one question, and a
// question saved again would flicker between them. Stray separators are read the
// way the judge reads them — an empty piece between two commas says nothing, so
// "A,,C" is the answer "A,C" and is not worth a round trip.
//
// The same goes for a key named twice. The stored answer is the set of keys that
// are right, and "A,A" is one of them: a model that repeated itself, or named
// one option in two spellings, has said exactly what "A" says. The convention
// the judge reads this column by is a list of distinct keys, so anything else
// would be a stored answer that means less than it says.
func readChoiceAnswer(answer string, options []notebook.Option) (string, error) {
	offered := make(map[string]string, len(options))
	spelling := make([]string, 0, len(options))
	for _, choice := range options {
		offered[foldKey(choice.Key)] = choice.Key
		spelling = append(spelling, choice.Key)
	}
	parts := cleanParts(answer, ",")
	keys := make([]string, 0, len(parts))
	for _, part := range parts {
		key, ok := offered[foldKey(part)]
		if !ok {
			return "", &Refusal{Note: fmt.Sprintf(
				"correct_answer 里的「%s」不在 options 里，options 提供的是 %s。",
				strings.TrimSpace(part), strings.Join(spelling, "、"))}
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return "", &Refusal{Note: "correct_answer 是空的。" +
			"选择题写选项键，多选写成 \"A,C\"（逗号分隔）。"}
	}
	sort.Strings(keys)
	return strings.Join(once(keys), ","), nil
}

// once drops the repeats from an ascending list of keys.
//
// It compares neighbours rather than keeping a set, because the list is already
// sorted and the keys are the options' own spellings by now: two pieces that
// named the same option, however each of them spelled it, are the same string.
func once(keys []string) []string {
	distinct := make([]string, 0, len(keys))
	for index, key := range keys {
		if index > 0 && key == keys[index-1] {
			continue
		}
		distinct = append(distinct, key)
	}
	return distinct
}

// cleanParts splits an answer the way the judge's reader does: on the separator,
// with the whitespace around each piece taken off and the pieces that say
// nothing dropped.
func cleanParts(answer, separator string) []string {
	parts := make([]string, 0, 2)
	for _, chunk := range strings.Split(answer, separator) {
		if trimmed := strings.TrimSpace(chunk); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return parts
}

// materialFor decides which material a question came from, and refuses a label
// this turn does not carry.
//
// The scope is the turn's and not the argument's: a label is looked up in the
// list the turn gave and nowhere else, so a question can only be filed under
// material the user put in front of this conversation (KB-07). A question that
// named no label but has exactly one attachment to have come from is recorded
// against it — the turn knows which material this was about even when the model
// did not say, and "the one file I gave you" is not a guess. Several attachments
// and no label is a question with no material: an arbitrary pick would be a
// citation of material the model never claimed.
func materialFor(carried []Carried, label string) (string, string, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		if len(carried) == 1 {
			return sourceAttachment, carried[0].MaterialID, nil
		}
		return "", "", nil
	}
	for _, attachment := range carried {
		if attachment.Label == label {
			return sourceAttachment, attachment.MaterialID, nil
		}
	}
	return "", "", &Refusal{Note: fmt.Sprintf(
		"本轮没有编号为「%s」的附件，携带的附件是 %s", label, labelsOf(carried))}
}

// labelsOf lists the numbers this turn's attachments carry, in the sentence the
// source tools refuse with, so that one mistake reads the same way wherever the
// model makes it.
func labelsOf(carried []Carried) string {
	if len(carried) == 0 {
		return "本轮没有附件"
	}
	labels := make([]string, 0, len(carried))
	for _, attachment := range carried {
		labels = append(labels, attachment.Label)
	}
	return strings.Join(labels, "、")
}

// foldKey folds an option key the way the identity of a key is decided.
//
// It is the judging path's fold (practice/objective.go's foldKey) written again,
// deliberately and not by accident: the two have to agree about which keys are
// the same key, and they cannot share the code because the judge is a domain
// package and this is the tool plane. It is narrower than the judge's in exactly
// one place — a full-width character is not folded to its half-width form — and
// the difference is only ever a refusal here: a model that wrote "Ａ" is told the
// key is not offered, and writes "A". On the judge's side the same character
// arrives from a user who cannot be told anything, which is why that side folds
// it and this one does not.
func foldKey(key string) string {
	var folded strings.Builder
	folded.Grow(len(key))
	for _, character := range strings.TrimSpace(key) {
		if unicode.IsSpace(character) {
			continue
		}
		folded.WriteRune(unicode.ToUpper(character))
	}
	return folded.String()
}

// knownDifficulty reports whether the difficulty is one of the three.
func knownDifficulty(difficulty string) bool {
	switch difficulty {
	case "easy", "medium", "hard":
		return true
	default:
		return false
	}
}

// tags cleans the free-text knowledge points: trimmed, with the blanks and the
// repeats dropped.
//
// The repeats are dropped because a tag is a label and not a count: the two
// places they are read — the filter that selects on them and the list shown
// beside a question — would both show "一元一次方程" twice, and neither could
// tell that from a user who really did mean it twice. Categories are left alone
// here because the bank cleans them itself, by name and by key.
func tags(names []string) []string {
	cleaned := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		cleaned = append(cleaned, trimmed)
	}
	return cleaned
}

// savedText is what the model reads back after a question lands.
//
// It says the two things the model cannot see: whether the question was new —
// the same question written twice is one question (§2.2), so "我再补一道" that
// repeats one already there has not added anything — and where the set stands,
// so that the model knows how many are left without having to count its own
// calls.
func savedText(saved Saved) string {
	what := "已存进题库"
	if !saved.Created {
		what = "题库里已经有这道题了，这次更新了它的题干"
	}
	if saved.Planned > 0 {
		return fmt.Sprintf("%s。这是第 %d/%d 道。", what, saved.Filed, saved.Planned)
	}
	return fmt.Sprintf("%s。这是第 %d 道。", what, saved.Filed)
}

// savedMetadata is what the tool result carries beside its text.
//
// Three of these keys are the progress keys (event/progress.go) and not a
// fourth set of names invented here: a client reads a question arriving and a
// plan advancing through one shape, which is what keeps "第 3/10 题" from being
// a thing the interface had to be taught separately. The label is the question's
// own text, since that is what a progress line names the step after.
func savedMetadata(saved Saved, question notebook.Question) map[string]any {
	return map[string]any{
		"notebook_entries_id": saved.EntryID,
		"question_id":         saved.QuestionID,
		"created":             saved.Created,
		event.MetaStep:        saved.Filed,
		event.MetaTotal:       saved.Planned,
		event.MetaLabel:       question.Text,
	}
}
