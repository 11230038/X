package notebook

// What the service decides before storage is asked anything: which questions are
// refused, that the address is computed here rather than taken from a caller, and
// that a request this version cannot act on is refused instead of answered
// differently than it was asked.
//
// The repository below is a record of the calls, not evidence about storage — the
// real database is that, in the tests beside this file. Its use here is the
// negative: a service that refuses a question before reaching storage can only be
// shown to have done so by a repository that would have noticed.

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

type fakeRepository struct {
	upserted []Question
	filed    []string
	filedFor []string
	// refiled is SetCategories' own record, kept apart from the filing a save
	// does: the two write the same thing to a different rule, and a test that
	// could not tell which of them ran would be testing neither.
	refiled    []string
	refiledFor []string
	marks      int
	listed     int
	// entries is the bank this repository holds, by content address. A bank
	// rather than a single entry, because what a test about a conflict needs is
	// the question that is already there and the one that is not.
	entries map[string]Entry

	upsertErr error
	fileErr   error
	markErr   error
	listErr   error
	entryErr  error
}

// withQuestion puts one question in the bank this repository holds.
func (f *fakeRepository) withQuestion(question Question) {
	if f.entries == nil {
		f.entries = map[string]Entry{}
	}
	question.QuestionID = QuestionID(question.Text, question.Type)
	f.entries[question.QuestionID] = Entry{Question: question}
}

func (f *fakeRepository) UpsertQuestion(_ context.Context, entryID string, question Question,
	_ time.Time) (Upserted, error) {
	if f.upsertErr != nil {
		return Upserted{}, f.upsertErr
	}
	f.upserted = append(f.upserted, question)
	return Upserted{ID: entryID, QuestionID: question.QuestionID, Created: true}, nil
}

func (f *fakeRepository) AddCategories(_ context.Context, entryID string, names []string) error {
	if f.fileErr != nil {
		return f.fileErr
	}
	f.filed = append(f.filed, names...)
	f.filedFor = append(f.filedFor, entryID)
	return nil
}

func (f *fakeRepository) Entry(_ context.Context, questionID string) (Entry, error) {
	if f.entryErr != nil {
		return Entry{}, f.entryErr
	}
	entry, ok := f.entries[questionID]
	if !ok {
		return Entry{}, fmt.Errorf("%w: %s", ErrNotFound, questionID)
	}
	return entry, nil
}

func (f *fakeRepository) List(_ context.Context, _ Filter) ([]Entry, error) {
	f.listed++
	return nil, f.listErr
}

func (f *fakeRepository) Mark(_ context.Context, _ string, _ Change) error {
	if f.markErr != nil {
		return f.markErr
	}
	f.marks++
	return nil
}

func (f *fakeRepository) SetCategories(_ context.Context, questionID string, names []string) error {
	if f.fileErr != nil {
		return f.fileErr
	}
	f.refiledFor = append(f.refiledFor, questionID)
	f.refiled = append(f.refiled, names...)
	return nil
}

func (f *fakeRepository) Categories(_ context.Context) ([]Category, error) {
	return nil, f.listErr
}

func newFakeService(t *testing.T) (*Service, *fakeRepository) {
	t.Helper()
	repository := &fakeRepository{}
	service, err := NewService(repository)
	if err != nil {
		t.Fatalf("new notebook service: %v", err)
	}
	return service, repository
}

func TestNewServiceNeedsARepository(t *testing.T) {
	if _, err := NewService(nil); err == nil {
		t.Fatal("a service over no repository was built")
	}
}

func TestSaveRefusesAQuestionStorageWouldRefuse(t *testing.T) {
	service, repository := newFakeService(t)
	cases := []struct {
		name     string
		question Question
		want     error
	}{
		{"a blank text", Question{Type: QuestionChoice}, ErrInvalidQuestion},
		{"a text of spaces", Question{Text: " \t\n ", Type: QuestionChoice}, ErrInvalidQuestion},
		{"an unknown type", Question{Text: "选一个", Type: QuestionType("essay")}, ErrInvalidQuestion},
		// Half a mastery coordinate. The point is an id inside one path's tree,
		// so either half on its own names somewhere that cannot be looked up.
		{"a knowledge point with no path",
			Question{Text: "选一个", Type: QuestionChoice, KnowledgePointID: "kp_001"},
			ErrInvalidQuestion},
		{"a path with no knowledge point",
			Question{Text: "选一个", Type: QuestionChoice, MasteryPathID: "mp_1"},
			ErrInvalidQuestion},
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			_, err := service.Save(context.Background(), scenario.question)
			if !errors.Is(err, scenario.want) {
				t.Fatalf("Save = %v, want %v", err, scenario.want)
			}
			if len(repository.upserted) != 0 || len(repository.filed) != 0 {
				t.Error("a question that was refused reached storage anyway")
			}
		})
	}
}

// TestSaveComputesTheAddressItself is the rule that makes "the same question"
// mean something: it is not a caller's to decide, and one that decided
// differently would be saving to a different question.
func TestSaveComputesTheAddressItself(t *testing.T) {
	service, repository := newFakeService(t)
	question := sampleQuestion("什么是闭包")
	question.QuestionID = "qn_这不是地址"
	if _, err := service.Save(context.Background(), question); err != nil {
		t.Fatalf("save: %v", err)
	}
	if len(repository.upserted) != 1 {
		t.Fatalf("storage was handed %d questions, want one", len(repository.upserted))
	}
	want := QuestionID(question.Text, question.Type)
	if got := repository.upserted[0].QuestionID; got != want {
		t.Errorf("stored under %s, want the address of the text and type, %s", got, want)
	}
}

// TestSaveFilesUnderTheRowStorageReported pins that the categories go on the row
// that is there rather than on the key the caller made up: a rewrite keeps the
// key it has, and the two are the same row.
func TestSaveFilesUnderTheRowStorageReported(t *testing.T) {
	repository := &fakeRepository{}
	service, err := NewService(&reportingRepository{fakeRepository: repository})
	if err != nil {
		t.Fatalf("new notebook service: %v", err)
	}
	question := withCategories(sampleQuestion("什么是闭包"), "数学")
	result, err := service.Save(context.Background(), question)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if result.ID != "nbe_已经在的" {
		t.Errorf("Save reported row %s, want the one storage reported", result.ID)
	}
	for _, entryID := range repository.filedFor {
		if entryID != "nbe_已经在的" {
			t.Errorf("the categories were filed under %s, want the row that is there", entryID)
		}
	}
}

// reportingRepository rewrites the row key on the way in, which is what the real
// adapter does when the question was already in the bank.
type reportingRepository struct {
	*fakeRepository
}

func (r *reportingRepository) UpsertQuestion(ctx context.Context, entryID string, question Question,
	now time.Time) (Upserted, error) {
	if _, err := r.fakeRepository.UpsertQuestion(ctx, entryID, question, now); err != nil {
		return Upserted{}, err
	}
	return Upserted{ID: "nbe_已经在的", QuestionID: question.QuestionID}, nil
}

func TestSaveReportsAFilingFailure(t *testing.T) {
	service, repository := newFakeService(t)
	repository.fileErr = errors.New("storage is down")
	if _, err := service.Save(context.Background(), sampleQuestion("什么是闭包")); err == nil {
		t.Fatal("a question that could not be filed was reported as saved")
	}
}

func TestServiceReportsWhatStorageSaid(t *testing.T) {
	service, repository := newFakeService(t)
	repository.upsertErr = errors.New("storage is down")
	if _, err := service.Save(context.Background(), sampleQuestion("什么是闭包")); err == nil {
		t.Fatal("a question that could not be stored was reported as saved")
	}
	repository = &fakeRepository{listErr: errors.New("storage is down")}
	service, err := NewService(repository)
	if err != nil {
		t.Fatalf("new notebook service: %v", err)
	}
	if _, err := service.List(context.Background(), Filter{}); err == nil {
		t.Fatal("a listing that could not be read was reported as read")
	}
	repository.markErr = errors.New("storage is down")
	yes := true
	if err := service.Mark(context.Background(), "qn_1", Change{Bookmarked: &yes}); err == nil {
		t.Fatal("a mark that could not be written was reported as written")
	}
}

func TestListRefusesAFilterThisVersionCannotActOn(t *testing.T) {
	service, repository := newFakeService(t)
	cases := []Filter{
		{Resolution: Resolution("maybe")},
		{Type: QuestionType("essay")},
	}
	for _, filter := range cases {
		if _, err := service.List(context.Background(), filter); !errors.Is(err, ErrInvalidFilter) {
			t.Errorf("List(%+v) = %v, want ErrInvalidFilter", filter, err)
		}
	}
	if repository.listed != 0 {
		t.Error("a filter that was refused reached storage anyway")
	}
}

func TestMarkRefusesAChangeThatNamesNothing(t *testing.T) {
	service, repository := newFakeService(t)
	if err := service.Mark(context.Background(), "qn_1", Change{}); !errors.Is(err, ErrEmptyChange) {
		t.Fatalf("Mark with nothing named = %v, want ErrEmptyChange", err)
	}
	if repository.marks != 0 {
		t.Error("a change with no subject reached storage anyway")
	}
}

// TestSetCategoriesAnswersAnEmptyList is the one thing this call decides above
// storage: unfiling a question is a request and not an error. The labels a
// question carries are the user's to set, and "none" is one of the values they
// can set them to.
func TestSetCategoriesAnswersAnEmptyList(t *testing.T) {
	service, repository := newFakeService(t)
	if err := service.SetCategories(context.Background(), "qn_1", nil); err != nil {
		t.Fatalf("unfile a question: %v", err)
	}
	if len(repository.refiledFor) != 1 || repository.refiledFor[0] != "qn_1" {
		t.Fatalf("storage was handed %v", repository.refiledFor)
	}
	if len(repository.refiled) != 0 {
		t.Errorf("an empty list was filed under %v", repository.refiled)
	}
}
