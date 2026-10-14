package turn

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"backend/internal/event"
	"backend/internal/kb"
	"backend/internal/parse"
	"backend/internal/tool"
)

// Material is what one turn was given to read.
//
// It is derived from what the client declared when it started the turn and from
// what this service could make of those declarations, and it is the only thing
// the source tools read. The model reaches material by asking about it, never by
// naming a file or a collection: an attachment is read under the label this turn
// gave it and a search runs over the collections this turn carries, so there is
// no argument a model could supply that would widen the scope it was handed
// (KB-07).
type Material struct {
	// Attachments are the files this turn carried, in the order they were
	// declared, one entry per declaration however the reading of it went.
	Attachments []Attachment
	// KnowledgeBases are the collections this turn may search. They are ids as
	// the client named them, and one that has since been deleted stays in the
	// list: what happened to it is reported, not papered over by leaving it out.
	KnowledgeBases []string
	// Selection is the passage a reader had chosen in a reading material, as the
	// source this turn cites it as, and SelectionNote is why there is none when
	// one was asked for.
	//
	// It is the one part of the material that is not searched: a selection is
	// what the question is about, and it is handed over whole rather than
	// retrieved from (§2.8). It mounts no tool — a client with a selection asks
	// about it rather than reading it through a call — which is why the plane
	// below is composed from the two lists and not from this.
	Selection     *event.Source
	SelectionNote string
}

// Attachment is one file a turn carried and whether it can be read.
type Attachment struct {
	// Label is what the model names this attachment by: its position in the
	// turn's own list, counted from 1.
	//
	// A position rather than a filename or a file id, because the model has to
	// be able to write it. A filename can belong to two attachments and is the
	// user's to choose; a file id is this service's bookkeeping, and handing one
	// to the model would be handing it something it could try to use. The
	// position is the only name that is both unambiguous and impossible to turn
	// into a way to read something else.
	Label string
	// FileID is the stored file, which is what a citation's locator opens.
	FileID string
	// Filename is the original name, which is what a reader recognises. It is
	// empty when the file could not be read at all.
	Filename string
	// DocumentID is the parsed document, empty when nothing was parsed.
	DocumentID string
	// Unavailable is why this attachment cannot be read, empty when it can.
	//
	// An attachment that could not be read is still in the list, with its
	// label and its reason: ATT-04 asks for the reason rather than for silence,
	// and a model that can say "我没有读到第 2 个附件，因为它是一张图" is useful
	// where one that says nothing is not.
	Unavailable string
}

// Materials is the part of the knowledge base domain the runtime uses: the
// material one turn was given.
//
// It is an interface for the same reason Sessions is one — a turn is testable
// without a database behind it — and it is held by the runtime rather than by a
// capability, which is what makes the turn, and not the capability, the place a
// scope lives. A capability that could reach the library would be a capability
// that could search outside what the user chose.
type Materials interface {
	// AddDocument parses a stored file into a document and chunks it. An empty
	// kbID is what makes it an attachment: the same parse, belonging to no
	// collection and retrievable by nobody.
	AddDocument(ctx context.Context, fileID, kbID string) (kb.Document, error)
	// ReadWindow reads a window of a document's text, clamped to what it has.
	ReadWindow(ctx context.Context, documentID string, offset, limit int64) (kb.Window, error)
	// Locate finds the passages of one document that answer a query. It stays
	// inside that document, which is why an attachment can be searched for
	// nothing but itself.
	Locate(ctx context.Context, documentID, query string, limit int) ([]event.Source, error)
	// Retrieve runs the hybrid search over a turn's collections.
	Retrieve(ctx context.Context, input kb.RetrieveInput) (kb.Retrieval, error)
}

var _ Materials = (*kb.Service)(nil)

// Declaration is what a client said a turn was carrying.
//
// It is kept on the turn run rather than on the turn row: it is what the turn
// needs while it runs, and what a conversation shows afterwards is the message's
// own record of it, not the turn's working state.
type Declaration struct {
	// Attachments are stored file ids.
	Attachments []string
	// KnowledgeBases are collection ids.
	KnowledgeBases []string
	// Selection is the passage a reader had chosen in a reading material when
	// this turn started, and it is nil for every turn that is not one (§2.8).
	// It rides here rather than in a field of its own because it is material:
	// it is what this turn was given to read, it is settled once, and the
	// survey is the only thing that reads it.
	Selection *Selection
}

// declarationOf reads what a client said a turn carries.
//
// A blank id is dropped rather than carried: it names nothing, so keeping it
// would spend one of the turn's attachment numbers on a file that can never be
// opened, and shift every number after it.
func declarationOf(attachments, knowledgeBases []string, selection *Selection) Declaration {
	return Declaration{
		Attachments:    namedIDs(attachments),
		KnowledgeBases: namedIDs(knowledgeBases),
		Selection:      selection,
	}
}

func namedIDs(ids []string) []string {
	kept := make([]string, 0, len(ids))
	for _, id := range ids {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			kept = append(kept, trimmed)
		}
	}
	return kept
}

// resolveMaterial turns this turn's declaration into what it may read.
//
// It never fails the turn. An attachment that could not be parsed is material
// that is missing, and a turn that can say so is worth more than one that stops:
// §2.3's rule that a provider being unwell costs recall and not correctness
// applies here word for word. Every declaration keeps its entry and its
// position, so a failure does not shift the numbering the model was given.
func (r *turnRun) resolveMaterial(ctx context.Context) {
	material := Material{KnowledgeBases: r.declared.KnowledgeBases}
	r.resolveSelection(ctx, &material)
	for index, fileID := range r.declared.Attachments {
		attachment := Attachment{Label: strconv.Itoa(index + 1), FileID: fileID}
		document, err := r.runner.materials.AddDocument(ctx, fileID, "")
		if err != nil {
			attachment.Unavailable = reasonFor(err)
			r.runner.logger.WarnContext(ctx, "could not read an attachment this turn carried",
				"turn_id", r.turn.ID, "file_id", fileID, "error", err)
		} else {
			attachment.DocumentID = document.ID
			attachment.Filename = document.Filename
		}
		material.Attachments = append(material.Attachments, attachment)
	}
	r.material = material
}

// resolveSelection turns the passage a reader had chosen into a source this turn
// can cite, and it is done here with the rest of the material for the reason the
// material is resolved here at all: the turn has one scope, settled once, and a
// selection that arrived later would be a second answer to what this turn is
// reading (§2.11).
//
// It never fails the turn. A selection that cannot be resolved — a material that
// has since gone, a span the document no longer has — is material that could not
// be read, reported the way an attachment that failed to parse is: the reader
// asked a question and the question still stands, so what is lost is the
// grounding and not the turn (§2.3). The numbering is untouched by the failure,
// because nothing was handed over.
func (r *turnRun) resolveSelection(ctx context.Context, material *Material) {
	if r.declared.Selection == nil || r.runner.readings == nil {
		return
	}
	source, err := r.runner.readings.Passage(ctx, *r.declared.Selection)
	if err != nil {
		material.SelectionNote = "你没有读到读者划出的那段原文：" + reasonFor(err)
		r.runner.logger.WarnContext(ctx, "could not resolve the selection this turn carried",
			"turn_id", r.turn.ID, "material_id", r.declared.Selection.MaterialID, "error", err)
		return
	}
	material.Selection = &source
}

// reasonFor explains a file this service could not read, in words a reader can
// act on.
//
// A parse failure already carries such a sentence — a parser that could not be
// reached says which setting turns it on, a format nobody reads says so — and it
// is passed through unchanged for that reason. Anything else is this service's
// own trouble, which a model could only repeat as a retry the user cannot make.
func reasonFor(err error) string {
	var failure *parse.Error
	if errors.As(err, &failure) {
		return failure.Message
	}
	return "读取这个附件时出错了，本轮没有读到它的内容"
}

// flags are the tool gates this turn's material opens.
//
// A turn with nothing returns nil, which is the request V1 sent: the plane such
// a turn gets is the one it got before there was material at all, name for name
// and in the same order.
func (m Material) flags() map[string]bool {
	if len(m.Attachments) == 0 && len(m.KnowledgeBases) == 0 {
		return nil
	}
	flags := make(map[string]bool, 2)
	if len(m.Attachments) > 0 {
		flags[tool.FlagAttachments] = true
	}
	if len(m.KnowledgeBases) > 0 {
		flags[tool.FlagKnowledgeBase] = true
	}
	return flags
}

// attachment finds one of this turn's attachments by the label the model used.
func (m Material) attachment(label string) (Attachment, bool) {
	for _, attachment := range m.Attachments {
		if attachment.Label == label {
			return attachment, true
		}
	}
	return Attachment{}, false
}

// readSource answers one read_source call.
//
// The label is looked up in this turn's own list and nowhere else, so an
// attachment belonging to another turn — or to no turn — is not a state this
// function has to consider: it is not in the list, and the read is refused.
func (r *turnRun) readSource(ctx context.Context, request tool.ReadRequest) (tool.Reading, error) {
	attachment, ok := r.material.attachment(request.Label)
	if !ok {
		// The refusal names what the turn does carry, because the model can only
		// correct itself if it is told which labels exist. It is a refusal and
		// not a read of something else: a label the turn never named is the model
		// reaching past the material it was given, and there is nothing behind it
		// to read (KB-07).
		return tool.Reading{}, &tool.Refusal{Note: fmt.Sprintf(
			"本轮没有编号为「%s」的附件，携带的附件是 %s", request.Label, r.material.labels())}
	}
	if attachment.Unavailable != "" {
		return tool.Reading{Unavailable: []string{unreadable(attachment)}}, nil
	}
	if request.Query != "" {
		sources, err := r.runner.materials.Locate(ctx, attachment.DocumentID, request.Query, 0)
		if err != nil {
			return tool.Reading{}, explainRefusal(err, "附件「"+attachment.Label+"」")
		}
		return tool.Reading{Sources: r.number(sources)}, nil
	}
	window, err := r.runner.materials.ReadWindow(ctx, attachment.DocumentID, request.Offset, request.Limit)
	if err != nil {
		return tool.Reading{}, err
	}
	if window.Text == "" {
		// Reading past the end of a document is a window at the end of it rather
		// than a failure (that is what the storage read promises), and the model
		// has to hear that the document stops before where it asked.
		return tool.Reading{Unavailable: []string{fmt.Sprintf(
			"附件「%s」（%s）一共只有 %d 个字符，从第 %d 个字符起没有内容",
			attachment.Label, attachment.Filename, window.TotalChars, request.Offset)}}, nil
	}
	return tool.Reading{Sources: r.number([]event.Source{window.Source(0, 0)})}, nil
}

// retrieve answers one retrieve call, over the collections this turn carries.
func (r *turnRun) retrieve(ctx context.Context, query string) (tool.Reading, error) {
	if len(r.material.KnowledgeBases) == 0 {
		// Unreachable through the tool plane, which mounts retrieve only for a
		// turn that carries a collection. Answered rather than refused, because a
		// read with no scope is a fact about the turn and not a defect.
		return tool.Reading{Unavailable: []string{"本轮没有携带知识库，没有可检索的来源"}}, nil
	}
	retrieval, err := r.runner.materials.Retrieve(ctx, kb.RetrieveInput{
		KBIDs: r.material.KnowledgeBases,
		Query: query,
	})
	if err != nil {
		return tool.Reading{}, explainRefusal(err, "本轮的知识库")
	}
	reading := tool.Reading{Sources: r.number(retrieval.Sources)}
	for _, kbID := range retrieval.Unavailable {
		reading.Unavailable = append(reading.Unavailable, goneKB(kbID))
	}
	return reading, nil
}

// goneKB is what a collection that is gone is reported as (KB-07).
//
// The id is the only name it still has — the row that held its name is the row
// that was deleted — so the id is what is reported, and nothing takes its place.
// Both readers of a scope say it this way: the model asking through the tool,
// and the survey the runtime runs before the first call.
func goneKB(kbID string) string {
	return fmt.Sprintf("知识库 %s 已经不存在，本轮没有检索它，也没有用别的来源替代它", kbID)
}

// explainRefusal turns a query the index cannot answer into something the model
// can act on, and lets every other failure be one.
//
// A query shorter than the full-text index's token size matches nothing at all,
// which would read as "the material does not mention this" when the truth is
// that the question could not be asked. The model can fix that by asking
// differently, so it is told rather than handed a failure.
func explainRefusal(err error, what string) error {
	if errors.Is(err, kb.ErrQueryTooShort) {
		return &tool.Refusal{Note: fmt.Sprintf("%s里无法用这么短的词检索，换一个更长、更具体的问法", what)}
	}
	return err
}

// labels lists the numbers this turn's attachments carry, for a refusal that
// has to tell the model what it could have asked for instead.
func (m Material) labels() string {
	if len(m.Attachments) == 0 {
		return "本轮没有附件"
	}
	labels := make([]string, 0, len(m.Attachments))
	for _, attachment := range m.Attachments {
		labels = append(labels, attachment.Label)
	}
	return strings.Join(labels, "、")
}

// number gives every source a number in this turn's own count.
//
// The count is the turn's, not the call's: a read that restarted at 1 would make
// [2] mean one passage in one answer and a different one in the next, and an
// answer that cites two reads would cite the same number twice for different
// things. Numbering here is also what keeps a citation honest — the numbers a
// model writes are the numbers it was handed, and nothing else in the turn
// invents one (AIQ-02).
func (r *turnRun) number(sources []event.Source) []event.Source {
	for index := range sources {
		r.sources++
		sources[index].Index = r.sources
	}
	return sources
}

// unreadable writes the reason an attachment could not be read, for the model.
func unreadable(attachment Attachment) string {
	if attachment.Filename == "" {
		return fmt.Sprintf("附件「%s」没有读到：%s", attachment.Label, attachment.Unavailable)
	}
	return fmt.Sprintf("附件「%s」（%s）没有读到：%s",
		attachment.Label, attachment.Filename, attachment.Unavailable)
}
