package main

import (
	"context"
	"fmt"

	"backend/internal/config"
	"backend/internal/mastery"
	"backend/internal/masterypath"
	"backend/internal/notebook"
	"backend/internal/practice"
	"backend/internal/tool"
	"backend/internal/turn"
	"gorm.io/gorm"
)

// masteryPath is the join between the mastery domain and the turn runtime.
//
// It has to exist because two things meet here and neither may know the other.
// The runtime asks once per turn which path that turn is on (turn.Paths), and it
// asks in the only vocabulary it has — the capability's name, the session, the
// turn, the question — because teaching it the word "path" would put the mastery
// domain inside the loop that runs every turn. The nine tools ask the path they
// were handed what they may read and write (tool.MasteryPath), and they ask in
// the domain's own vocabulary because that is what they are written in.
//
// This is the same seam the source tools, the question bank and the router use:
// what a turn calls is declared in the turn package, implemented elsewhere, and
// joined here. It is also the only place that knows both halves of §2.3 — that a
// turn's path is a lease some turn holds, and that the tools are the thing which
// has to behave differently when it is somebody else's.
type masteryPath struct {
	service *mastery.Service
	// bank is the runtime's own question plane, so a question filed on a path is
	// stamped and numbered exactly as one filed by save_question: one code path,
	// one counter, one meaning for "第 N 道" (§2.5).
	bank   tool.QuestionBank
	reader masterypath.QuestionReader
	judge  masterypath.Judge
}

var _ turn.Paths = masteryPath{}

// Claim settles which path this turn is on and prepares its tools for it.
//
// A turn that is not running the mastery capability is not on a path, and the
// answer is the context it came in with and no claim at all — which is what every
// turn in this build got before this version. The check is the capability's name
// and nothing else, because that is the whole of what the runtime knows: which
// capabilities work on a path is a fact about the capability, and it is written
// down once, in the package that declares it.
//
// An error here is a turn that never starts. The one case that looks like a
// refusal and is not is a path another turn is writing: Arrive reports the holder
// rather than failing, the turn runs and reads, and its writes come back with the
// holder's name in them (§2.3). What does fail is a request the domain cannot
// carry out at all — a conversation already in the middle of as many paths as a
// person may hold, or storage that will not answer.
func (m masteryPath) Claim(ctx context.Context, request turn.ClaimRequest) (turn.Claimed, error) {
	if request.Capability != masterypath.Name {
		return turn.Claimed{Context: ctx}, nil
	}
	actor := mastery.Actor{SessionID: request.SessionID, TurnID: request.TurnID}
	// The account travels into the domain with the rest of the turn: it is what
	// every read and write the tools make is scoped to, and what a path this turn
	// creates is owned by. It reaches the tools the same way the path does — it is
	// carried on the TurnPath that is put on the context — because they call the
	// domain themselves and none of them has another way to know who is asking.
	arrival, err := m.service.Arrive(ctx, mastery.ArriveRequest{Actor: actor, Goal: request.Goal}, request.Caller)
	if err != nil {
		return turn.Claimed{}, err
	}
	path := masterypath.NewTurnPath(m.service, m.bank, m.reader, m.judge,
		arrival.Path.ID, actor, request.Caller, arrival.Path.Version, arrival.Ours, arrival.Lease)
	return turn.Claimed{
		Context: tool.WithMasteryPath(ctx, path),
		Lease:   arrival.Metadata(),
	}, nil
}

// newMasteryPaths builds the mastery domain and the join onto the runtime.
//
// It is where the four keys of §5 become the domain's four thresholds, and it is
// a constructor rather than a block inside assemble for the same reason the
// capability list is: what the domain is tuned with can then be read on its own.
//
// It hands back the join rather than the interface it satisfies, because the
// process keeps both halves: the runtime reaches the domain through Claim, and
// the HTTP endpoints reach the same *service* for the reads and the renames a
// person does outside a turn. One service, two ways in — the same arrangement as
// the knowledge base.
func newMasteryPaths(
	cfg config.Config,
	db *gorm.DB,
	questions *notebook.Service,
	judge *practice.Judge,
) (masteryPath, error) {
	service, err := mastery.NewService(mastery.NewGORMRepository(db), mastery.Options{
		LeaseTTL: cfg.Mastery.LeaseTTL,
		Gate: mastery.GateOptions{
			Consecutive: cfg.Mastery.GateConsecutive,
			MinEvidence: cfg.Mastery.GateMinEvidence,
		},
		MaxActivePaths: cfg.Mastery.MaxActivePaths,
	})
	if err != nil {
		return masteryPath{}, fmt.Errorf("initialize the mastery path domain: %w", err)
	}
	// The bank is the runtime's, and it is the runtime's own type: a question
	// filed from inside a path carries this turn's session and turn and is
	// numbered in this turn's count, exactly as save_question's would be.
	return masteryPath{service: service, bank: turn.Banker{}, reader: questions, judge: judge}, nil
}
