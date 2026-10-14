package masterypath

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"backend/internal/mastery"
	"backend/internal/notebook"
	"backend/internal/practice"
	"backend/internal/tool"
)

// The bridge: what the nine mastery tools call through.
//
// The tools are declared in `internal/tool` and know nothing about the domain
// beyond the port they were written against (tool.MasteryPath). This type is
// that port implemented over a mastery service, and it is here rather than in
// `internal/tool` for the same reason the capability is (see the package
// comment): the file that would hold it imports the domain that `tool` may not.
//
// It is one per turn, because a path, an actor and the version this turn last
// read are one per turn. It is handed to the turn's context by the runtime and
// found there by each tool call (tool.WithMasteryPath).
//
// Two of the dependencies are not the domain and are worth naming. The bank is
// the runtime's own question plane, so a question filed from inside a path is
// stamped and numbered exactly as one filed by save_question — one code path, one
// counter, one meaning for "第 N 道". The judge is practice's, unchanged: §2.5
// says mastery_grade reuses that judging wholesale, which is what keeps an
// objective answer from costing a model call.
type TurnPath struct {
	service *mastery.Service
	bank    tool.QuestionBank
	reader  QuestionReader
	judge   Judge

	pathID string
	actor  mastery.Actor
	// version is the version this turn last read, and it is what the next write
	// is guarded by. It moves forward after every successful write, so a turn
	// that makes three edits in a row is not refused by its own first one.
	version int64
	// held reports whether this turn's arrival took the lease, and holder is who
	// has it when it did not. Together they are §2.3's first mechanism: a turn
	// that did not get the lease still reads, still asks and still judges, and
	// every write it attempts says who is writing instead.
	//
	// It is the arrival's answer and not a question asked again at each call, and
	// that is deliberate: what happens when the lease moves *during* a turn is the
	// version guard's business (a lease that expired and was taken over leaves a
	// late writer with a stale version, and evidence still lands — §2.3's last
	// paragraph). Asking the lease table again at every tool call would be the
	// same answer read a second time, and a second place where "who holds it"
	// could disagree with what the turn announced in its first event.
	held   bool
	holder mastery.Lease
}

// writable refuses a write when the path belongs to another turn.
//
// It is one sentence in one place because §2.3's refusal is one thing: the model
// is told who is writing, that reading goes on, and the two ways out. A model
// that got "conflict" could only give up, and a learner with two tabs open is not
// a learner whose second tab should be dead.
func (t *TurnPath) writable() error {
	if t.held {
		return nil
	}
	return &tool.Refusal{Note: fmt.Sprintf(
		"这条路径现在由另一个 turn（%s，会话 %s）在写，你改不动它。"+
			"可以照常读、出题、判卷；要改的话先问用户是等它写完，还是用 mastery_switch 换一条路径。",
		t.holder.TurnID, t.holder.SessionID)}
}

var _ tool.MasteryPath = (*TurnPath)(nil)

// QuestionReader reads one question back out of the bank. A verdict has to be
// made against the stored row — a choice question's accepted spellings live
// there and nowhere else — so the bridge reads it rather than trusting what the
// model said the question was.
type QuestionReader interface {
	Get(ctx context.Context, questionID string) (notebook.Entry, error)
}

var _ QuestionReader = (*notebook.Service)(nil)

// Judge is practice's judging, as much of it as this bridge needs.
type Judge interface {
	Judge(ctx context.Context, question notebook.Question, userAnswer string) (practice.Judgment, error)
}

var _ Judge = (*practice.Judge)(nil)

// NewTurnPath returns the bridge for one turn on one path.
//
// held and holder are the arrival's answer about the lease (§2.3): a turn that
// did not get it gets a bridge that reads and refuses writes.
func NewTurnPath(
	service *mastery.Service,
	bank tool.QuestionBank,
	reader QuestionReader,
	judge Judge,
	pathID string,
	actor mastery.Actor,
	version int64,
	held bool,
	holder mastery.Lease,
) *TurnPath {
	return &TurnPath{
		service: service,
		bank:    bank,
		reader:  reader,
		judge:   judge,
		pathID:  pathID,
		actor:   actor,
		version: version,
		held:    held,
		holder:  holder,
	}
}

// Carried lists this turn's attachments, under the labels the source tools
// number them by. It is the runtime's own list: a question filed on a path
// carries the same materials as one filed anywhere else, and the labels the
// model reads must not depend on which tool it called.
func (t *TurnPath) Carried(ctx context.Context) []tool.Carried {
	return t.bank.Carried(ctx)
}

// Current reads the path this turn is on.
func (t *TurnPath) Current(ctx context.Context) (mastery.View, error) {
	view, err := t.refresh(ctx)
	if err != nil {
		return mastery.View{}, t.refuse(err)
	}
	return view, nil
}

// Ask files a question against one knowledge point of this turn's path.
//
// The order is the bank first, the path second, and it is the order the two
// records depend on each other in: the interaction names a question by the id
// the bank gave it, so the question has to exist before the path can be told
// that it was asked. The cost of the order is a question in the bank that no
// path recorded — which is why the coordinate is checked in front of the bank
// rather than beside the path: a question is an asset of the bank on its own
// (QN-01) and would be a fine thing to keep, but this one has been stamped with
// a knowledge point, and a stamp is not a thing that can be corrected later.
//
// This is also the one place that holds both halves of a question's mastery
// coordinate at once — the path is the turn's, the knowledge point is the
// model's — so it is where the pair is written onto the question before the bank
// stores it (§2.2). Nothing else may fill those two columns: a question is filed
// once, by the call that generated it on the path it was generated for.
func (t *TurnPath) Ask(ctx context.Context, question notebook.Question, knowledgePointID string) (tool.Saved, error) {
	// Before the bank, because an interaction is a write to the path: a question
	// filed by a turn that may not write the path would be a question in the bank
	// that the path never recorded, and the model would be told it asked
	// something the path knows nothing about.
	if err := t.writable(); err != nil {
		return tool.Saved{}, err
	}
	// And the coordinate is checked here, before the stamp is written, because the
	// domain's own check happens a moment too late: by the time mastery.Ask reads
	// the tree, the question is in the bank carrying an id nothing resolves. §2.2
	// says the database cannot verify a knowledge point — it points into
	// state_json — so the only place that can refuse one is the tree that defines
	// it, and the only moment that matters is before the column is filled.
	//
	// The tool makes this same check one call earlier, and that is not this one
	// twice: its refusal lists the ids the path does have, which is what a model
	// needs to try again. This one is the guarantee, and it does not depend on
	// which caller got here first.
	view, err := t.refresh(ctx)
	if err != nil {
		return tool.Saved{}, t.refuse(err)
	}
	if _, _, ok := view.Path.Tree.KnowledgePoint(knowledgePointID); !ok {
		return tool.Saved{}, t.refuse(fmt.Errorf("%w: knowledge point %s is not in path %s",
			mastery.ErrNotFound, knowledgePointID, t.pathID))
	}
	question.MasteryPathID = t.pathID
	question.KnowledgePointID = knowledgePointID
	saved, err := t.bank.Save(ctx, question)
	if err != nil {
		return tool.Saved{}, err
	}
	if _, err := t.service.Ask(ctx, mastery.AskRequest{
		PathID:           t.pathID,
		Actor:            t.actor,
		KnowledgePointID: knowledgePointID,
		QuestionID:       saved.QuestionID,
	}); err != nil {
		return tool.Saved{}, t.refuse(err)
	}
	// The question is on the path now, so its version has moved: it is read back
	// so the next write is guarded by the version that is actually there.
	if _, err := t.refresh(ctx); err != nil {
		return tool.Saved{}, t.refuse(err)
	}
	return saved, nil
}

// Grade judges one answer and records what the verdict says about the knowledge
// point.
//
// The verdict is not the model's: the question is read back out of the bank and
// put to practice's judge, whose objective branch compares against the stored
// answer without calling a model at all (§2.5, V3 §2.5). What the model gets
// back is the verdict and the judge's sentence — not the rubric, not the
// confidence, not the boundary: a model handed those would start explaining a
// judgement it did not make.
func (t *TurnPath) Grade(ctx context.Context, questionID, userAnswer string) (tool.Graded, error) {
	// A judgement closes an interaction and writes evidence, so it is a write
	// like any other here. It is deliberately not exempted on the grounds that
	// the answer really happened: what would happen is that a second window's
	// turn moves the path's levels while the first window is mid-sentence, and
	// the learner is looking at two different answers to the same question.
	if err := t.writable(); err != nil {
		return tool.Graded{}, err
	}
	entry, err := t.reader.Get(ctx, questionID)
	if err != nil {
		return tool.Graded{}, t.refuse(err)
	}
	judgment, err := t.judge.Judge(ctx, entry.Question, userAnswer)
	if err != nil {
		return tool.Graded{}, err
	}
	detail, err := json.Marshal(judgment)
	if err != nil {
		return tool.Graded{}, fmt.Errorf("encode the judgement of %s: %w", questionID, err)
	}

	result := mastery.ResultIncorrect
	if judgment.IsCorrect {
		result = mastery.ResultCorrect
	}
	if _, err := t.service.Grade(ctx, mastery.GradeRequest{
		PathID:         t.pathID,
		Actor:          t.actor,
		QuestionID:     questionID,
		UserAnswer:     userAnswer,
		Result:         result,
		Detail:         detail,
		AssessmentType: string(judgment.Kind),
	}); err != nil {
		return tool.Graded{}, t.refuse(err)
	}
	if _, err := t.refresh(ctx); err != nil {
		return tool.Graded{}, t.refuse(err)
	}
	return tool.Graded{Correct: judgment.IsCorrect, Reason: judgment.Reason}, nil
}

// Observe records one observation about one knowledge point.
//
// The observation is an assessment by construction: the tool that reports a
// graded answer is Grade, and this one is the model saying what it saw. The two
// are told apart in the row because a later reader — the insight endpoint, and
// whoever is asking why a level is what it is — has to be able to.
func (t *TurnPath) Observe(ctx context.Context, observation tool.Observation) (mastery.View, error) {
	if err := t.writable(); err != nil {
		return mastery.View{}, err
	}
	detail, err := json.Marshal(map[string]any{"detail": observation.Detail})
	if err != nil {
		return mastery.View{}, fmt.Errorf("encode the observation of %s: %w", observation.KnowledgePointID, err)
	}
	if _, err := t.service.RecordEvidence(ctx, mastery.Evidence{
		PathID:         t.pathID,
		KnowledgePoint: observation.KnowledgePointID,
		Result:         observation.Result,
		Source:         mastery.SourceAssessment,
		Detail:         detail,
		Actor:          t.actor,
	}); err != nil {
		return mastery.View{}, t.refuse(err)
	}
	view, err := t.refresh(ctx)
	if err != nil {
		return mastery.View{}, t.refuse(err)
	}
	return view, nil
}

// Skip records that the learner moved past one knowledge point.
//
// It writes no evidence, and nothing here may change that: an observation would
// move the gate, and a learner who said "先放着" would come back to find
// themselves marked bad at it (§2.5).
func (t *TurnPath) Skip(ctx context.Context, knowledgePointID, reason string) error {
	if err := t.writable(); err != nil {
		return err
	}
	if err := t.service.Skip(ctx, mastery.SkipRequest{
		PathID:           t.pathID,
		Actor:            t.actor,
		KnowledgePointID: knowledgePointID,
		Reason:           reason,
	}); err != nil {
		return t.refuse(err)
	}
	if _, err := t.refresh(ctx); err != nil {
		return t.refuse(err)
	}
	return nil
}

// Map merges modules into the tree and reports the tree as it now stands.
func (t *TurnPath) Map(ctx context.Context, modules []mastery.Module) (mastery.View, error) {
	if err := t.writable(); err != nil {
		return mastery.View{}, err
	}
	if _, err := t.service.Map(ctx, mastery.MapRequest{
		Change:  t.change(),
		Modules: modules,
	}); err != nil {
		return mastery.View{}, t.refuse(err)
	}
	view, err := t.refresh(ctx)
	if err != nil {
		return mastery.View{}, t.refuse(err)
	}
	return view, nil
}

// Edit rewords one knowledge point, moves it, or both.
func (t *TurnPath) Edit(ctx context.Context, edit tool.KnowledgePointEdit) (mastery.View, error) {
	if err := t.writable(); err != nil {
		return mastery.View{}, err
	}
	if _, err := t.service.EditKnowledgePoint(ctx, mastery.EditKnowledgePointRequest{
		Change:   t.change(),
		ID:       edit.ID,
		Edit:     edit.Edit,
		ToModule: edit.ToModule,
		Position: edit.Position,
	}); err != nil {
		return mastery.View{}, t.refuse(err)
	}
	view, err := t.refresh(ctx)
	if err != nil {
		return mastery.View{}, t.refuse(err)
	}
	return view, nil
}

// Switch moves this turn onto another path and reports that path's state.
//
// The path this bridge follows moves with the turn: after a switch, every later
// call — a question, a judgement, a leave — is about the path it arrived at,
// which is what makes the tool a switch rather than a read. So does the claim:
// the switch acquires the path it arrives at, so a turn that was refused writes
// here can switch somewhere free and write there, which is the way out the
// refusal names.
//
// It is the one write not gated on this turn's own lease, and it cannot be: the
// lease it has to pass is the destination's, and that one is checked by the
// domain's Acquire, which reports the holder the same way (§2.3).
func (t *TurnPath) Switch(ctx context.Context, pathID string) (mastery.View, error) {
	view, err := t.service.Switch(ctx, mastery.SwitchRequest{
		Actor: t.actor,
		From:  t.pathID,
		To:    pathID,
	})
	if err != nil {
		return mastery.View{}, t.refuse(err)
	}
	t.pathID = view.Path.ID
	t.version = view.Path.Version
	t.held = true
	t.holder = mastery.Lease{}
	return view, nil
}

// Leave releases this turn's claim on the path it is on.
//
// Releasing a path this turn does not hold is not an error: the lease may have
// expired and been taken over while the turn ran, and a turn that has finished
// with a path should be able to say so either way.
func (t *TurnPath) Leave(ctx context.Context) error {
	if _, err := t.service.Release(ctx, t.pathID, t.actor); err != nil {
		return t.refuse(err)
	}
	return nil
}

// change is the guard every tree write goes through.
func (t *TurnPath) change() mastery.Change {
	expected := t.version
	return mastery.Change{PathID: t.pathID, Actor: t.actor, Expected: &expected}
}

// refresh re-reads the path and moves this turn's version up to what it reads.
func (t *TurnPath) refresh(ctx context.Context) (mastery.View, error) {
	view, err := t.service.Load(ctx, t.pathID)
	if err != nil {
		return mastery.View{}, err
	}
	t.version = view.Path.Version
	return view, nil
}

// refuse turns a domain error into the sentence the model reads.
//
// The tools only understand two kinds of failure: a *tool.Refusal, which is a
// sentence handed back to the model, and anything else, which is a defect the
// turn reports and fails on. Everything the domain refuses with is the first
// kind — a lease somebody else holds, a question already waiting, a path that
// moved on while the turn was reading it — and the reason they are translated
// here rather than in `internal/tool` is that only the domain knows which of its
// errors are refusals and which are faults. A storage failure is not a sentence
// for a model: it is passed through untouched.
func (t *TurnPath) refuse(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, mastery.ErrLeaseHeld):
		var held *mastery.HeldError
		if errors.As(err, &held) {
			return &tool.Refusal{Note: fmt.Sprintf(
				"这条路径现在由另一个 turn（%s，会话 %s）在写，你改不动它。"+
					"可以照常读、出题、判卷；要改的话先问用户是等它写完，还是用 mastery_switch 换一条路径。",
				held.Lease.TurnID, held.Lease.SessionID)}
		}
		return &tool.Refusal{Note: "这条路径现在由另一个 turn 在写，你改不动它。"}
	case errors.Is(err, mastery.ErrOpenInteraction):
		var open *mastery.OpenInteractionError
		if errors.As(err, &open) {
			return &tool.Refusal{Note: fmt.Sprintf(
				"这条路径上已经有一道题在等作答（%s，知识点 %s）：先把用户对它的回答交给 mastery_grade，"+
					"或者跟用户说清楚这道题还开着，不要另出一题。",
				open.Interaction.ID, open.Interaction.KnowledgePointID)}
		}
		return &tool.Refusal{Note: "这条路径上已经有一道题在等作答：先把它判掉，再出下一题。"}
	case errors.Is(err, mastery.ErrStaleState):
		return &tool.Refusal{Note: "这条路径在你读之后被改过了。先调一次 mastery_state 读回现在的树和版本，再照着重来一遍。"}
	case errors.Is(err, mastery.ErrNotFound):
		return &tool.Refusal{Note: "没找到：" + err.Error() + "。" +
			"知识点要用 mastery_state 里那条知识点的 id（kp_ 开头），路径要用 mp_ 开头的 id。"}
	case errors.Is(err, mastery.ErrDuplicate):
		return &tool.Refusal{Note: "这条记录已经写过了，不用再写一遍。"}
	case errors.Is(err, mastery.ErrInvalidRequest):
		return &tool.Refusal{Note: "这次调用不能这么写：" + err.Error() + "。"}
	default:
		return err
	}
}
