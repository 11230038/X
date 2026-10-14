package notebook

// One line of an import file, and what it has to be to be a question this bank
// takes (§2.13). The file's format, the report and the walk are in import.go;
// this is the reader.
//
// A line is the shape save_question's arguments have, because the two doors onto
// this bank have to store the same questions: a file whose line this reader
// refuses and a call the tool accepts would make the bank's rules depend on which
// door a question came in through. The bar below is therefore the tool's bar,
// written here a second time deliberately — the same reason the tool's foldKey is
// written again rather than shared: internal/tool imports this package, so the
// two cannot be one function, and the choice is between a written-out copy and
// two doors that disagree.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The bar a line has to clear, and the bounds a choice question is written
// under. They are save_question's own numbers (its MinQuestionRunes, its
// MinExplanationRunes, its option bounds), and they are the numbers the judge
// reads questions by: an explanation is what a short answer is judged against,
// and a question offering one option offers nothing to choose between.
const (
	importMinQuestionRunes    = 4
	importMinExplanationRunes = 2
	importMinOptions          = 2
	importMaxOptions          = 8
)

// importDifficulties is the closed set the difficulty column holds, listed the
// way a refusal names it.
var importDifficulties = []string{"easy", "medium", "hard"}

// importLine is one line of the file: the fields save_question's arguments have,
// named as a client names them.
//
// The material is named by `source` and `material_id` rather than by the tool's
// `source_label`, and that is the one field that cannot be the same: a label is a
// position in one conversation's list of attachments, and a file has no
// conversation to be a position in. The two columns are what a label resolved to
// anyway.
type importLine struct {
	Question        string         `json:"question"`
	QuestionType    string         `json:"question_type"`
	Options         []importOption `json:"options"`
	CorrectAnswer   string         `json:"correct_answer"`
	Explanation     string         `json:"explanation"`
	Difficulty      string         `json:"difficulty"`
	Source          string         `json:"source"`
	MaterialID      string         `json:"material_id"`
	KnowledgePoints []string       `json:"knowledge_points"`
	Categories      []string       `json:"categories"`
}

// importOption is one choice as a line writes it.
type importOption struct {
	Key  string `json:"key"`
	Text string `json:"text"`
}

// readImportLine reads one line as a question, or says why it is not one.
//
// It decides only what the line itself says — is this a question, and is it a
// question with an answer — and never what the bank holds. That is the
// classification's business (import.go), and it is the whole reason the reading
// is one function: a preview and a commit that disagreed about which lines are
// questions would disagree about everything after that.
//
// The address comes back filled in only for a line that is a question this bank
// would store. Everything else gets no address, because an address computed from
// a question the bank refuses names the row of a different question.
func readImportLine(number int, raw string) (ImportEntry, Question) {
	entry := ImportEntry{Line: number, Outcome: ImportInvalid}
	parsed, err := decodeImportLine(raw)
	if err != nil {
		entry.Reason = err.Error()
		return entry, Question{}
	}
	question := Question{
		Text:          strings.TrimSpace(parsed.Question),
		Type:          QuestionType(strings.TrimSpace(parsed.QuestionType)),
		CorrectAnswer: parsed.CorrectAnswer,
		Explanation:   strings.TrimSpace(parsed.Explanation),
		Difficulty:    strings.TrimSpace(parsed.Difficulty),
		Source:        strings.TrimSpace(parsed.Source),
		MaterialID:    strings.TrimSpace(parsed.MaterialID),
		Categories:    parsed.Categories,
	}
	// Knowledge points are cleaned by the rule categories are cleaned by —
	// trimmed, emptied, de-duplicated — because they are the same kind of thing: a
	// list of names a writer repeats itself in, stored as a list of names.
	question.KnowledgePoints = cleanCategoryNames(parsed.KnowledgePoints)

	// Storage's own bar first, then the two things the rest of the build needs:
	// a question the judge can find the answer to, and an explanation the card can
	// show. The order is the order a question is read in, so that the reason a
	// line comes back with is the first thing wrong with it rather than the last.
	if err := question.Validate(); err != nil {
		entry.Reason = importReason(err, ErrInvalidQuestion)
		return entry, Question{}
	}
	if runes := utf8.RuneCountInString(question.Text); runes < importMinQuestionRunes {
		entry.Reason = fmt.Sprintf("the question text is %d characters long, and a question is at least %d",
			runes, importMinQuestionRunes)
		return entry, Question{}
	}
	if strings.TrimSpace(parsed.CorrectAnswer) == "" {
		entry.Outcome = ImportMissingAnswer
		entry.Reason = "the line names no correct answer, and a question nobody can answer is not a question"
		return entry, Question{}
	}
	options, err := readImportOptions(parsed.Options, question.Type)
	if err != nil {
		entry.Reason = err.Error()
		return entry, Question{}
	}
	question.Options = options
	answer, err := readImportAnswer(parsed.CorrectAnswer, question.Type, options)
	if err != nil {
		entry.Reason = err.Error()
		return entry, Question{}
	}
	question.CorrectAnswer = answer
	if runes := utf8.RuneCountInString(question.Explanation); runes < importMinExplanationRunes {
		entry.Reason = fmt.Sprintf(
			"the explanation is %d characters long, and a question is at least %d: "+
				"a short answer is judged by it, and the card shows it after the verdict",
			runes, importMinExplanationRunes)
		return entry, Question{}
	}
	if question.Difficulty != "" && !knownImportDifficulty(question.Difficulty) {
		entry.Reason = fmt.Sprintf("%q is not a difficulty; the three are %s",
			question.Difficulty, strings.Join(importDifficulties, ", "))
		return entry, Question{}
	}
	// The address is computed here rather than read from the line: a file does not
	// get to name its question, which is the rule Service.Save follows for a
	// caller too. The format has no field for one, and this is why.
	question.QuestionID = QuestionID(question.Text, question.Type)
	entry.Outcome = ImportWritable
	entry.QuestionID = question.QuestionID
	return entry, question
}

// importReason is the part of a domain error's text that follows its sentinel:
// the sentence worth showing. The sentinel is a package name and a category, and
// a report that repeated it would be telling the reader which package refused
// their line.
func importReason(err error, sentinel error) string {
	prefix := sentinel.Error() + ": "
	text := err.Error()
	if strings.HasPrefix(text, prefix) {
		return strings.TrimPrefix(text, prefix)
	}
	return text
}

func knownImportDifficulty(difficulty string) bool {
	return slices.Contains(importDifficulties, difficulty)
}

// decodeImportLine reads one line as a JSON object.
//
// A field nobody reads is refused rather than ignored: a line is a contract, and
// a misspelled one — `answer` for `correct_answer` — would otherwise arrive as a
// question with no answer, which is a confusing way to be told about a typo. A
// second JSON value on the same line is refused for the same reason: two
// questions on one line is a file this format does not have, and reading the
// first and dropping the second is how a question goes missing without anybody
// noticing.
func decodeImportLine(raw string) (importLine, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var parsed importLine
	if err := decoder.Decode(&parsed); err != nil {
		return importLine{}, fmt.Errorf("the line is not a question: %w", err)
	}
	if err := decoder.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return importLine{}, errors.New("the line holds more than one JSON value")
	}
	return parsed, nil
}

// readImportOptions reads the choices of a choice question, and refuses options
// on the kinds that have none.
//
// Two options that fold together are one option to a reader and one row in an
// answer, so a question offering both can only ever be answered with one of the
// two. The fold is the tool's (whitespace taken off, upper-cased) and
// deliberately not the judge's, which folds full-width characters as well: a
// question written through a file has to be the question a call would have
// written, and closing the gap between the two folds is a change to both doors at
// once rather than to one of them.
func readImportOptions(given []importOption, questionType QuestionType) ([]Option, error) {
	if questionType != QuestionChoice {
		if len(given) > 0 {
			return nil, fmt.Errorf("%s questions have no options, and this line carries %d",
				questionType, len(given))
		}
		return nil, nil
	}
	if len(given) < importMinOptions || len(given) > importMaxOptions {
		return nil, fmt.Errorf("a choice question offers between %d and %d options, and this line carries %d",
			importMinOptions, importMaxOptions, len(given))
	}
	options := make([]Option, 0, len(given))
	seen := make(map[string]bool, len(given))
	for _, choice := range given {
		key := strings.TrimSpace(choice.Key)
		text := strings.TrimSpace(choice.Text)
		if key == "" || text == "" {
			return nil, fmt.Errorf("an option is missing its %s", missingOptionPart(key, text))
		}
		folded := foldImportKey(key)
		if seen[folded] {
			return nil, fmt.Errorf("two options are keyed %q, and option keys are what an answer names", key)
		}
		seen[folded] = true
		options = append(options, Option{Key: key, Text: text})
	}
	return options, nil
}

// missingOptionPart names the half of an option that is not there, which is the
// whole of what the writer has to change.
func missingOptionPart(key, text string) string {
	switch {
	case key == "" && text == "":
		return "key and text"
	case key == "":
		return "key"
	default:
		return "text"
	}
}

// readImportAnswer reads the answer in the form the question's type stores it,
// which is the form the judge reads it back in — the same conventions
// save_question's description tells a model about, because they are the judge's
// conventions and not the writer's.
func readImportAnswer(answer string, questionType QuestionType, options []Option) (string, error) {
	switch questionType {
	case QuestionChoice:
		return readImportChoiceAnswer(answer, options)
	case QuestionFillBlank:
		alternatives := cleanParts(answer, "|")
		if len(alternatives) == 0 {
			return "", errors.New(
				"the fill-in answer says nothing; write the text, or the accepted spellings joined with \"|\"")
		}
		return strings.Join(alternatives, "|"), nil
	default:
		return strings.TrimSpace(answer), nil
	}
}

// readImportChoiceAnswer reads a choice answer as the option keys it names,
// spelled the way the options spell them, each once, in ascending order.
//
// The order is the storage convention, so that "A,C" and "C,A" are one stored
// answer rather than two, and the spelling is the option's own, so that a file
// answering "a" and one answering "A" are one answer to one question.
func readImportChoiceAnswer(answer string, options []Option) (string, error) {
	offered := make(map[string]string, len(options))
	spelling := make([]string, 0, len(options))
	for _, choice := range options {
		offered[foldImportKey(choice.Key)] = choice.Key
		spelling = append(spelling, choice.Key)
	}
	keys := make([]string, 0, len(options))
	for _, part := range cleanParts(answer, ",") {
		key, ok := offered[foldImportKey(part)]
		if !ok {
			return "", fmt.Errorf("the answer names %q, which is not one of the options: %s",
				part, strings.Join(spelling, ", "))
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return "", errors.New("the choice answer names no option key")
	}
	slices.Sort(keys)
	distinct := make([]string, 0, len(keys))
	for _, key := range keys {
		if len(distinct) == 0 || key != distinct[len(distinct)-1] {
			distinct = append(distinct, key)
		}
	}
	return strings.Join(distinct, ","), nil
}

// foldImportKey folds an option key the way this reader decides which keys are
// the same key.
func foldImportKey(key string) string {
	var folded strings.Builder
	for _, character := range strings.TrimSpace(key) {
		if unicode.IsSpace(character) {
			continue
		}
		folded.WriteRune(unicode.ToUpper(character))
	}
	return folded.String()
}

// cleanParts splits an answer on its separator, the way the judge's reader does:
// whitespace around each piece taken off, and the pieces that say nothing
// dropped.
func cleanParts(answer, separator string) []string {
	parts := make([]string, 0, 2)
	for _, piece := range strings.Split(answer, separator) {
		if trimmed := strings.TrimSpace(piece); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return parts
}
