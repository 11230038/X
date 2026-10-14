package turn

import (
	"backend/internal/authz"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"backend/internal/event"
	"backend/internal/kb"
	"backend/internal/llm"
	"backend/internal/parse"
	"backend/internal/tool"
)

// stubMaterials stands in for the knowledge base domain: reads come out of what
// a test put in, and every read is written down, so a test can tell a read that
// was refused from one that never happened.
type stubMaterials struct {
	// files maps a stored file id to the document parsing it produces.
	files map[string]kb.Document
	// fileErrs maps a stored file id to the failure reading it produces, which is
	// how a test says "this attachment could not be read".
	fileErrs map[string]error
	// windows maps a document id to the window a read of it returns.
	windows map[string]kb.Window
	// located and locateErr are what a locate of an attachment returns.
	located   []event.Source
	locateErr error
	// retrieved and retrieveErr are what a search of the collections returns.
	retrieved   kb.Retrieval
	retrieveErr error

	// asked is every read that reached the domain, in order.
	asked []string
}

var _ Materials = (*stubMaterials)(nil)

func newStubMaterials() *stubMaterials {
	return &stubMaterials{
		files:    make(map[string]kb.Document),
		fileErrs: make(map[string]error),
		windows:  make(map[string]kb.Window),
	}
}

// add gives a stored file id a document, so a turn that carries it can read it.
// The document answers a whole-document read with a line naming its file, which
// is enough for a test that is about which read happened rather than what came
// back; a test about the text sets the window itself afterwards.
func (m *stubMaterials) add(fileID, documentID, filename string) {
	document := kb.Document{ID: documentID, FileID: fileID, Filename: filename}
	m.files[fileID] = document
	m.windows[documentID] = kb.Window{
		Document:   document,
		Text:       filename + " 的内容",
		CharEnd:    int64(len([]rune(filename))) + 4,
		TotalChars: int64(len([]rune(filename))) + 4,
	}
}

func (m *stubMaterials) AddDocument(_ context.Context, fileID, kbID string, _ authz.Caller) (kb.Document, error) {
	m.asked = append(m.asked, "add "+fileID)
	if err, ok := m.fileErrs[fileID]; ok {
		return kb.Document{}, err
	}
	document, ok := m.files[fileID]
	if !ok {
		return kb.Document{}, fmt.Errorf("stub: no document for %s", fileID)
	}
	return document, nil
}

func (m *stubMaterials) ReadWindow(_ context.Context, documentID string, _, _ int64, _ authz.Caller) (kb.Window, error) {
	m.asked = append(m.asked, "window "+documentID)
	window, ok := m.windows[documentID]
	if !ok {
		return kb.Window{}, fmt.Errorf("stub: no window for %s", documentID)
	}
	return window, nil
}

func (m *stubMaterials) Locate(_ context.Context, documentID, query string, _ int, _ authz.Caller) ([]event.Source, error) {
	m.asked = append(m.asked, "locate "+documentID+" "+query)
	return m.located, m.locateErr
}

func (m *stubMaterials) Retrieve(_ context.Context, input kb.RetrieveInput) (kb.Retrieval, error) {
	m.asked = append(m.asked, "retrieve "+strings.Join(input.KBIDs, ",")+" "+input.Query)
	return m.retrieved, m.retrieveErr
}

// source is one returned passage, so a test does not have to build the whole of
// event.Source to say "the read found this".
func source(documentID, snippet string) event.Source {
	return event.Source{DocumentID: documentID, Snippet: snippet}
}

func TestFlagsComeFromWhatTheTurnCarries(t *testing.T) {
	for _, test := range []struct {
		name     string
		material Material
		want     map[string]bool
	}{
		{
			name: "a turn carrying nothing opens no gate",
			// nil rather than an empty map: this is the request V1 sent, and a
			// turn with no material has to get the plane it got before there was
			// material at all.
			want: nil,
		},
		{
			name:     "attachments",
			material: Material{Attachments: []Attachment{{Label: "1", FileID: "lib_a"}}},
			want:     map[string]bool{tool.FlagAttachments: true},
		},
		{
			name:     "collections",
			material: Material{KnowledgeBases: []string{"kb_a"}},
			want:     map[string]bool{tool.FlagKnowledgeBase: true},
		},
		{
			name: "both",
			material: Material{
				Attachments:    []Attachment{{Label: "1", FileID: "lib_a"}},
				KnowledgeBases: []string{"kb_a"},
			},
			want: map[string]bool{tool.FlagAttachments: true, tool.FlagKnowledgeBase: true},
		},
		{
			name: "an attachment that could not be read still opens its gate",
			// The gate is about what the turn carries, not about what could be
			// made of it: read_source is how the model is told the attachment is
			// unreadable, and a turn that mounted nothing would leave it with no
			// way to find that out.
			material: Material{Attachments: []Attachment{{Label: "1", FileID: "lib_a", Unavailable: "不支持"}}},
			want:     map[string]bool{tool.FlagAttachments: true},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := test.material.flags()
			if len(got) != len(test.want) {
				t.Fatalf("flags() = %v, want %v", got, test.want)
			}
			for flag, set := range test.want {
				if got[flag] != set {
					t.Errorf("flags()[%q] = %v, want %v", flag, got[flag], set)
				}
			}
		})
	}
}

func TestThePlaneFollowsWhatTheTurnCarries(t *testing.T) {
	policy := tool.Policy{
		Gates: []tool.Gate{
			{Flag: tool.FlagKnowledgeBase, Tools: []string{"retrieve"}},
			{Flag: tool.FlagAttachments, Tools: []string{"read_source"}},
		},
		AlwaysOn: []string{tool.AskUserName},
	}
	for _, test := range []struct {
		name    string
		request StartRequest
		want    []string
	}{
		{
			name:    "a turn carrying nothing",
			request: StartRequest{Content: "在吗"},
			// Name for name and in the order V1 sent them: the gate table moves
			// nothing for a turn that opens no gate.
			want: []string{tool.AskUserName},
		},
		{
			name:    "attachments only",
			request: StartRequest{Content: "看这份笔记", Attachments: []string{"lib_a"}},
			want:    []string{"read_source", tool.AskUserName},
		},
		{
			name:    "collections only",
			request: StartRequest{Content: "查一下", KnowledgeBases: []string{"kb_a"}},
			want:    []string{"retrieve", tool.AskUserName},
		},
		{
			name: "both",
			request: StartRequest{
				Content: "对照着看", Attachments: []string{"lib_a"}, KnowledgeBases: []string{"kb_a"},
			},
			// The gate table's own order, which is the order the design fixes.
			want: []string{"retrieve", "read_source", tool.AskUserName},
		},
		{
			name:    "an attachment that could not be read",
			request: StartRequest{Content: "看这份", Attachments: []string{"lib_missing"}},
			// Still mounted: read_source is how the model finds out that the
			// attachment is unreadable, and a turn that dropped it would leave the
			// model with no way to be told (ATT-04).
			want: []string{"read_source", tool.AskUserName},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRunnerFixture(t, func(options *Options) { options.Policy = policy })
			fixture.provider.responses = []llm.Response{{Content: "好"}}
			for _, name := range []string{"read_source", "retrieve"} {
				if err := fixture.tools.Register(&stubTool{name: name}); err != nil {
					t.Fatalf("registering %s: %v", name, err)
				}
			}
			fixture.register(t, looping("chat", agentRequest()))

			started := fixture.start(t, test.request)
			fixture.repository.awaitStatus(t, started.Turn.ID, StatusCompleted)

			asked := fixture.provider.asked()
			if len(asked) == 0 {
				t.Fatal("the model was never called")
			}
			offered := make([]string, 0, len(asked[0].Tools))
			for _, definition := range asked[0].Tools {
				offered = append(offered, definition.Name)
			}
			if len(offered) != len(test.want) {
				t.Fatalf("the model was offered %v, want %v", offered, test.want)
			}
			for index := range test.want {
				if offered[index] != test.want[index] {
					t.Fatalf("the model was offered %v, want %v", offered, test.want)
				}
			}
		})
	}
}

func TestATurnReadsOnlyWhatItCarries(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.materials.add("lib_mine", "doc_mine", "笔记.md")
	run := &turnRun{runner: fixture.runner}

	// A turn carrying nothing resolves to nothing, and reading an attachment
	// from it is refused: this is the scope rule at its narrowest, and it is the
	// state most turns are in.
	run.declared = Declaration{}
	run.resolveMaterial(context.Background())
	if len(run.material.Attachments) != 0 {
		t.Fatalf("a turn that declared nothing carries %v", run.material.Attachments)
	}
	if _, err := run.readSource(context.Background(), tool.ReadRequest{Label: "1"}); err == nil {
		t.Error("read_source on a turn carrying nothing was not refused")
	}

	// With one attachment, that one is readable under its own number and the
	// next number is not.
	run.declared = Declaration{Attachments: []string{"lib_mine"}}
	run.resolveMaterial(context.Background())
	if len(run.material.Attachments) != 1 || run.material.Attachments[0].Label != "1" {
		t.Fatalf("material = %+v, want one attachment numbered 1", run.material.Attachments)
	}
	if run.material.Attachments[0].DocumentID != "doc_mine" {
		t.Errorf("the attachment did not resolve to its document: %+v", run.material.Attachments[0])
	}
	if reading, err := run.readSource(context.Background(), tool.ReadRequest{Label: "1"}); err != nil {
		t.Errorf("read_source(1) error = %v", err)
	} else if len(reading.Sources) == 0 {
		t.Error("read_source(1) found nothing")
	}
	_, err := run.readSource(context.Background(), tool.ReadRequest{Label: "2"})
	var refusal *tool.Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("read_source(an attachment this turn has not got) error = %v, want a refusal", err)
	}
	// The refusal says which numbers do exist, because a model told only "no"
	// has nothing to correct itself with.
	if !strings.Contains(refusal.Note, "1") {
		t.Errorf("the refusal does not name what the turn carries: %q", refusal.Note)
	}
	for _, asked := range fixture.materials.asked {
		if strings.HasPrefix(asked, "add lib_other") {
			t.Errorf("a refused read still reached the domain: %v", fixture.materials.asked)
		}
	}
}

func TestAnAttachmentThatCouldNotBeReadKeepsItsNumberAndItsReason(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.materials.fileErrs["lib_bad"] = &parse.Error{
		Code:    parse.CodeParserUnavailable,
		Message: "这台服务器上的 PDF 解析服务当前不可用，请稍后重试或改用文本文件。",
	}
	run := &turnRun{runner: fixture.runner}
	// A picture among the attachments: the one before it must keep number 1 and
	// the one after must keep number 3, or the model's numbers stop meaning what
	// it was told they mean.
	fixture.materials.add("lib_good", "doc_good", "笔记.md")
	run.declared = Declaration{Attachments: []string{"lib_good", "lib_bad", "lib_also_good"}}
	fixture.materials.add("lib_also_good", "doc_also_good", "提纲.md")
	run.resolveMaterial(context.Background())

	if len(run.material.Attachments) != 3 {
		t.Fatalf("material = %+v, want one entry per declaration", run.material.Attachments)
	}
	for index, want := range []string{"1", "2", "3"} {
		if run.material.Attachments[index].Label != want {
			t.Errorf("attachment %d is numbered %q, want %q", index, run.material.Attachments[index].Label, want)
		}
	}
	unreadableOne := run.material.Attachments[1]
	if unreadableOne.Unavailable == "" {
		t.Fatal("the attachment that could not be parsed says nothing about why")
	}
	// The parser's own sentence is what a reader can act on, so it is passed
	// through rather than replaced by this service's account of it.
	if unreadableOne.Unavailable != "这台服务器上的 PDF 解析服务当前不可用，请稍后重试或改用文本文件。" {
		t.Errorf("the reason = %q, want the parser's own words", unreadableOne.Unavailable)
	}

	// Reading it is not a failure: it is a read that reports what it could not
	// give, which is what the model repeats to the user (ATT-04).
	reading, err := run.readSource(context.Background(), tool.ReadRequest{Label: "2"})
	if err != nil {
		t.Fatalf("read_source(an unreadable attachment) error = %v", err)
	}
	if len(reading.Sources) != 0 || len(reading.Unavailable) != 1 {
		t.Fatalf("reading = %+v, want a note and no sources", reading)
	}
	if !strings.Contains(reading.Unavailable[0], "PDF 解析服务") {
		t.Errorf("the note = %q, want it to carry the reason", reading.Unavailable[0])
	}
}

func TestSourcesAreNumberedOnceForTheWholeTurn(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.materials.add("lib_a", "doc_a", "a.md")
	fixture.materials.add("lib_b", "doc_b", "b.md")
	// Two reads that each number from 1 on their own would leave [2] meaning one
	// passage in the first half of an answer and a different one in the second.
	fixture.materials.located = []event.Source{source("doc_a", "第一段"), source("doc_a", "第二段")}
	fixture.materials.windows["doc_b"] = kb.Window{
		Document: kb.Document{ID: "doc_b", FileID: "lib_b", Filename: "b.md"},
		Text:     "b 的开头",
	}
	run := &turnRun{runner: fixture.runner}
	run.declared = Declaration{Attachments: []string{"lib_a", "lib_b"}}
	run.resolveMaterial(context.Background())

	first, err := run.readSource(context.Background(), tool.ReadRequest{Label: "1", Query: "中值定理"})
	if err != nil {
		t.Fatalf("read_source(1, query) error = %v", err)
	}
	second, err := run.readSource(context.Background(), tool.ReadRequest{Label: "2"})
	if err != nil {
		t.Fatalf("read_source(2) error = %v", err)
	}
	got := []int{first.Sources[0].Index, first.Sources[1].Index, second.Sources[0].Index}
	want := []int{1, 2, 3}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("the numbers a turn hands out are %v, want %v", got, want)
		}
	}
}

func TestReadingPastTheEndOfAnAttachmentSaysSoWithoutFailing(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.materials.add("lib_a", "doc_a", "a.md")
	// Past the end is an empty window at the end, which is what the storage read
	// gives rather than an error — so this is the state the note has to be made
	// out of.
	fixture.materials.windows["doc_a"] = kb.Window{
		Document:   kb.Document{ID: "doc_a", FileID: "lib_a", Filename: "a.md"},
		CharStart:  4,
		CharEnd:    4,
		TotalChars: 4,
	}
	run := &turnRun{runner: fixture.runner}
	run.declared = Declaration{Attachments: []string{"lib_a"}}
	run.resolveMaterial(context.Background())

	reading, err := run.readSource(context.Background(), tool.ReadRequest{Label: "1", Offset: 4000})
	if err != nil {
		t.Fatalf("read_source(past the end) error = %v, want a note rather than a failure", err)
	}
	if len(reading.Sources) != 0 || len(reading.Unavailable) != 1 {
		t.Fatalf("reading = %+v, want a note and no sources", reading)
	}
	if !strings.Contains(reading.Unavailable[0], "4") {
		t.Errorf("the note = %q, want it to say how long the document is", reading.Unavailable[0])
	}
}

func TestAQueryTooShortToSearchIsToldRatherThanFailed(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.materials.located = nil
	fixture.materials.locateErr = fmt.Errorf("%w: it needs at least 2 characters", kb.ErrQueryTooShort)
	run := &turnRun{runner: fixture.runner, material: Material{
		Attachments: []Attachment{{Label: "1", FileID: "lib_a", DocumentID: "doc_a"}},
	}}
	_, err := run.readSource(context.Background(), tool.ReadRequest{Label: "1", Query: "中"})
	var refusal *tool.Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("read_source(a query the index cannot match) error = %v, want a refusal", err)
	}
	// A refusal is what the model can act on; the generic tool failure is not.
	if !strings.Contains(refusal.Note, "更长") {
		t.Errorf("the refusal = %q, want it to say how to ask differently", refusal.Note)
	}

	if _, err := run.readSource(context.Background(), tool.ReadRequest{Label: "1", Query: "中"}); err == nil {
		t.Error("the same query twice was not refused the second time")
	}
}

func TestACollectionThatIsGoneIsReportedAndNotReplaced(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.materials.retrieved = kb.Retrieval{
		Sources:     []event.Source{source("doc_a", "还在的那个库")},
		Unavailable: []string{"kb_gone"},
	}
	run := &turnRun{runner: fixture.runner, material: Material{KnowledgeBases: []string{"kb_here", "kb_gone"}}}

	reading, err := run.retrieve(context.Background(), "拉格朗日")
	if err != nil {
		t.Fatalf("retrieve() error = %v", err)
	}
	// KB-07: the scope is the turn's, so a missing base is named rather than
	// quietly dropped — dropping it would let the answer look complete.
	if len(reading.Unavailable) != 1 || !strings.Contains(reading.Unavailable[0], "kb_gone") {
		t.Errorf("unavailable = %v, want the missing base named", reading.Unavailable)
	}
	if len(reading.Sources) != 1 || reading.Sources[0].Index != 1 {
		t.Errorf("sources = %+v, want the one the search found, numbered from 1", reading.Sources)
	}
	// The search was asked for exactly the turn's collections, and it is the
	// domain's answer that says one of them is gone.
	if want := "retrieve kb_here,kb_gone 拉格朗日"; fixture.materials.asked[0] != want {
		t.Errorf("the search was asked for %v, want %q", fixture.materials.asked, want)
	}
}

func TestATurnWithNoCollectionsSearchesNothing(t *testing.T) {
	fixture := newRunnerFixture(t)
	run := &turnRun{runner: fixture.runner}
	reading, err := run.retrieve(context.Background(), "拉格朗日")
	if err != nil {
		t.Fatalf("retrieve() error = %v", err)
	}
	if len(reading.Sources) != 0 || len(reading.Unavailable) != 1 {
		t.Fatalf("reading = %+v, want a note and no sources", reading)
	}
	// Not a fallback to some default collection, and not a search at all: a turn
	// that named no collection has nothing to search (KB-07).
	if len(fixture.materials.asked) != 0 {
		t.Errorf("the domain was asked %v, want nothing", fixture.materials.asked)
	}
}

func TestTheSourceToolsFindTheTurnTheyAreCalledFor(t *testing.T) {
	fixture := newRunnerFixture(t)
	fixture.materials.add("lib_a", "doc_a", "a.md")
	run := &turnRun{runner: fixture.runner}
	run.declared = Declaration{Attachments: []string{"lib_a"}}
	run.resolveMaterial(context.Background())

	ctx := context.WithValue(context.Background(), turnKey{}, run)
	if _, err := (Sourcer{}).ReadSource(ctx, tool.ReadRequest{Label: "1"}); err != nil {
		t.Errorf("ReadSource() error = %v", err)
	}
	// Outside a turn there is no material to read, and the tools say so rather
	// than reaching for a library they were never given.
	if _, err := (Sourcer{}).ReadSource(context.Background(), tool.ReadRequest{Label: "1"}); err == nil {
		t.Error("ReadSource() outside a turn was not refused")
	}
	if _, err := (Sourcer{}).Retrieve(context.Background(), "拉格朗日"); err == nil {
		t.Error("Retrieve() outside a turn was not refused")
	}
}

func TestBlankIDsAreNotCarried(t *testing.T) {
	// An empty id names nothing, so it must not take one of the turn's numbers.
	// The selection rides through untouched: it is a passage and not an id, so
	// there is nothing in it for this to trim or drop (§2.8).
	selection := &Selection{MaterialID: "rm_a", Locator: event.Locator{Ordinal: 3, CharStart: 10, CharEnd: 20}}
	declared := declarationOf([]string{"  ", "lib_a", ""}, []string{"", " kb_a "}, selection)
	if len(declared.Attachments) != 1 || declared.Attachments[0] != "lib_a" {
		t.Errorf("attachments = %v, want only the one that names something", declared.Attachments)
	}
	if len(declared.KnowledgeBases) != 1 || declared.KnowledgeBases[0] != "kb_a" {
		t.Errorf("knowledge bases = %v, want only the one that names something", declared.KnowledgeBases)
	}
	if declared.Selection != selection {
		t.Errorf("selection = %+v, want the one that was declared", declared.Selection)
	}
}
