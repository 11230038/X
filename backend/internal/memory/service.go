package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The prefixes of the row ids this package makes. They are readable on purpose:
// a fact id turns up in a URL and in an audit row, and a reader looking at both
// should not have to guess which is which.
const (
	traceIDPrefix = "mt_"
	factIDPrefix  = "mf_"
	auditIDPrefix = "ma_"
	idRandomBytes = 16
)

// Options are the six knobs the memory layers have, all of them from the
// deployment (§5).
//
// Four of the six are the product decision §2.7 names: how sure a model has to
// claim to be, how many observations count as corroboration, how many places
// they have to come from, and which kinds of fact are never admitted without the
// user. The mechanism would be the same for any numbers; which numbers is not a
// question code can answer, which is why they are here and not constants.
type Options struct {
	// ConfidenceThreshold is the lowest confidence, 0–100, that passes the first
	// condition. Zero is a legitimate setting — a deployment that wants to rely on
	// corroboration alone — which is why the bound is a range rather than
	// "positive".
	ConfidenceThreshold int
	// MinCorroboration is how many independent observations one statement needs
	// before it becomes a fact.
	MinCorroboration int
	// MinSurfaces is how many different surfaces those observations must come
	// from. This is the condition "corroborated" actually rests on.
	MinSurfaces int
	// SensitiveKinds are the kinds that never pass the gate, whatever their
	// confidence and whatever corroborates them.
	SensitiveKinds []string
	// SynthesisWindowDays is how far back L3's "recent" reaches.
	SynthesisWindowDays int
	// MaxFactsPerSurface is how many facts one surface contributes to a
	// synthesis, which is the bound that keeps one talkative surface from being
	// the whole picture.
	MaxFactsPerSurface int
}

// Validate reports whether the options are usable.
//
// It checks what the configuration section checks, and that is deliberate rather
// than duplicated by accident: the configuration refuses a bad deployment file
// at startup with the key's name in the message, and this refuses to build a
// service whose rules could not run. The two are reached by different callers —
// a test builds options by hand — so neither can be dropped in favour of the
// other.
func (o Options) Validate() error {
	if o.ConfidenceThreshold < 0 || o.ConfidenceThreshold > 100 {
		return fmt.Errorf("memory: the confidence threshold must be between 0 and 100, not %d", o.ConfidenceThreshold)
	}
	for _, knob := range []struct {
		name  string
		value int
	}{
		{"the corroboration minimum", o.MinCorroboration},
		{"the surface minimum", o.MinSurfaces},
		{"the synthesis window", o.SynthesisWindowDays},
		{"the per-surface fact bound", o.MaxFactsPerSurface},
	} {
		if knob.value <= 0 {
			return fmt.Errorf("memory: %s must be positive, not %d", knob.name, knob.value)
		}
	}
	for _, kind := range o.SensitiveKinds {
		if strings.TrimSpace(kind) == "" {
			return fmt.Errorf("memory: the sensitive kinds must not carry an empty kind")
		}
	}
	return nil
}

// Service owns the rules about memory: what an observation means, what it takes
// for one to become a fact, and what a merge does to the facts it folds
// together.
type Service struct {
	repository Repository
	options    Options
	// clock is a field and not a call to time.Now inside the methods, because
	// every write here stores a time and a case that cannot choose the time
	// cannot pin the row it wrote.
	clock func() time.Time
}

// NewService returns the service over a storage adapter.
func NewService(repository Repository, options Options) (*Service, error) {
	if repository == nil {
		return nil, fmt.Errorf("memory service needs a repository")
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	return &Service{
		repository: repository,
		options:    options,
		clock:      func() time.Time { return time.Now().UTC() },
	}, nil
}

// Observe records one observation and asks the gate what it means (§2.7).
//
// The order inside is the whole of the contract:
//
//  1. the observation is written to L1 first, whatever happens next. An
//     observation is not a proposal to be accepted or rejected — it is a record
//     that a surface said something, and MEM-03's second state (heard, not yet
//     believed) has to exist before it can be shown;
//  2. if the account already believes the same thing, the observation joins that
//     fact as evidence instead of starting a second one. This is what makes the
//     evidence chain grow rather than duplicate;
//  3. otherwise the gate decides, and a "yes" writes the fact with every
//     matching observation as its evidence.
//
// Two of these can run at once for one account and one statement — two turns
// observing the same thing is not exotic — and both can find no fact and both
// can be told yes. Nothing here prevents that, and nothing should: this engine
// has no partial unique index that would say "one fact in force per fingerprint"
// without also forbidding the fact that comes *after* a retraction. The repair
// is Dedupe, which exists for exactly this and says so.
func (s *Service) Observe(ctx context.Context, observation Observation) (Observed, error) {
	if err := observation.Validate(); err != nil {
		return Observed{}, err
	}
	traceID, err := newID(traceIDPrefix)
	if err != nil {
		return Observed{}, err
	}
	factID, err := newID(factIDPrefix)
	if err != nil {
		return Observed{}, err
	}
	fingerprint := Fingerprint(observation.Statement)
	observed := Observed{TraceID: traceID, Proposed: true}

	err = s.repository.Within(ctx, func(tx Repository) error {
		trace := Trace{
			ID:          traceID,
			OwnerID:     observation.OwnerID,
			Surface:     observation.Surface,
			Kind:        observation.Kind,
			SessionID:   observation.SessionID,
			TurnID:      observation.TurnID,
			Statement:   observation.Statement,
			Fingerprint: fingerprint,
			Payload:     observation.Payload,
			CreatedAt:   s.clock(),
		}
		if err := tx.AppendTrace(ctx, trace); err != nil {
			return err
		}

		believed, err := tx.ActiveFact(ctx, observation.OwnerID, fingerprint)
		if err == nil {
			if err := tx.AttachSource(ctx, believed.ID, traceID, s.clock()); err != nil {
				return err
			}
			observed = Observed{TraceID: traceID, FactID: believed.ID, Admitted: true}
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}

		stored, err := tx.Traces(ctx, TraceQuery{OwnerID: observation.OwnerID, Fingerprint: fingerprint})
		if err != nil {
			return err
		}
		decision := decide(observation, stored, s.options)
		if !decision.admitted {
			return nil
		}
		fact := Fact{
			ID:          factID,
			OwnerID:     observation.OwnerID,
			Surface:     observation.Surface,
			Scope:       observation.Scope,
			Statement:   observation.Statement,
			Fingerprint: fingerprint,
			Confidence:  decision.confidence,
			Status:      StatusActive,
			Sensitive:   decision.sensitive,
			ConfirmedBy: decision.confirmedBy,
			Version:     1,
			CreatedAt:   s.clock(),
		}
		if err := tx.CreateFact(ctx, fact); err != nil {
			return err
		}
		for _, source := range decision.sources {
			if err := tx.AttachSource(ctx, fact.ID, source, s.clock()); err != nil {
				return err
			}
		}
		observed = Observed{TraceID: traceID, FactID: fact.ID, Admitted: true}
		return nil
	})
	if err != nil {
		return Observed{}, err
	}
	return observed, nil
}

// Traces reads one account's L1.
func (s *Service) Traces(ctx context.Context, query TraceQuery) ([]Trace, error) {
	if err := query.validate(); err != nil {
		return nil, err
	}
	return s.repository.Traces(ctx, query)
}

// Facts reads one account's L2.
func (s *Service) Facts(ctx context.Context, query FactQuery) ([]Fact, error) {
	if err := query.validate(); err != nil {
		return nil, err
	}
	return s.repository.Facts(ctx, query)
}

// Fact reads one fact and the observations it rests on.
//
// The fact is read first and the evidence second, so a fact that is not there
// fails before anything else is read; and the evidence is read for a fact that
// is there, which is why the two cannot disagree about whose fact it is.
func (s *Service) Fact(ctx context.Context, ownerID int64, factID string) (FactWithEvidence, error) {
	if ownerID <= 0 || strings.TrimSpace(factID) == "" {
		return FactWithEvidence{}, fmt.Errorf("%w: which fact of which account is not said", ErrInvalidRequest)
	}
	fact, err := s.repository.Fact(ctx, ownerID, factID)
	if err != nil {
		return FactWithEvidence{}, err
	}
	traces, err := s.repository.TracesOf(ctx, factID)
	if err != nil {
		return FactWithEvidence{}, err
	}
	return FactWithEvidence{Fact: fact, Traces: traces}, nil
}

// Audits reads one account's audit trail: what has been done to this memory, by
// whom, and when (MEM-02).
func (s *Service) Audits(ctx context.Context, query AuditQuery) ([]Audit, error) {
	if err := query.validate(); err != nil {
		return nil, err
	}
	return s.repository.Audits(ctx, query)
}

// DedupeRequest is a request to merge the facts that say the same thing.
type DedupeRequest struct {
	OwnerID int64
	// DryRun reports what the merge would do and writes nothing. It is the same
	// shape QN-05's import takes (§2.13), and the reason is the same: a merge is
	// not something a caller should have to discover afterwards.
	DryRun bool
	// Actor is who asked (audit.go). Blank is refused rather than defaulted: an
	// audit row saying who did something is the point of the audit row.
	Actor Actor
}

// DedupeGroup is one set of facts that say the same thing, and what happened to
// them.
type DedupeGroup struct {
	Fingerprint string
	// Statement is the survivor's wording: the facts agree on the fingerprint,
	// which is the statement with its whitespace folded, so one of them is what
	// the other one says.
	Statement string
	// Survivor is the fact the others folded into: the oldest of the group. A
	// merge is an absorption, and the fact that was there first keeps its id, so
	// whatever already points at it still does.
	Survivor string
	// Superseded are the facts that stopped being in force, in the order the
	// merge took them. On a preview they are the ones it would take.
	Superseded []string
}

// DedupeReport is what a merge found and what it did.
type DedupeReport struct {
	DryRun bool
	Groups []DedupeGroup
	// Superseded is how many facts stopped being in force. It is zero on a
	// preview, which is what makes the preview's report comparable with the
	// commit's rather than merely similar.
	Superseded int
	// Result is the word the audit row carries: ResultPartial when a group lost a
	// race — somebody edited a fact while it was being merged — and that fact was
	// left alone rather than overwritten. A preview cannot race and always says
	// ResultDone.
	Result Result
}

// Dedupe merges the facts in force that say the same thing.
//
// This is the repair for the one thing Observe cannot prevent. Two observations
// of the same statement arriving at once each find no fact and each admit one,
// because the rule "one fact in force per fingerprint" is not expressible as a
// constraint here: a unique index would also forbid the fact that comes after a
// retraction, and a partial index — unique only over the active rows — is not
// something this engine has (§0.2). So the duplicates are found afterwards and
// folded, which is also why the merge needs a preview: it is not a formality
// here, it is the only look a caller gets before two rows become one.
//
// The evidence moves rather than being copied or dropped. The observations are
// the same observations either way, and MEM-01's chain is a chain of them: after
// a merge the survivor rests on everything both facts rested on.
func (s *Service) Dedupe(ctx context.Context, request DedupeRequest) (DedupeReport, error) {
	if request.OwnerID <= 0 {
		return DedupeReport{}, fmt.Errorf("%w: the owner is %d", ErrInvalidRequest, request.OwnerID)
	}
	if strings.TrimSpace(string(request.Actor)) == "" {
		return DedupeReport{}, fmt.Errorf("%w: the actor is blank", ErrInvalidRequest)
	}
	report := DedupeReport{DryRun: request.DryRun, Groups: []DedupeGroup{}, Result: ResultDone}

	err := s.repository.Within(ctx, func(tx Repository) error {
		groups, err := tx.DuplicateGroups(ctx, request.OwnerID)
		if err != nil {
			return err
		}
		for _, group := range groups {
			if len(group.Facts) < 2 {
				continue
			}
			merged := DedupeGroup{
				Fingerprint: group.Fingerprint,
				Statement:   group.Facts[0].Statement,
				Survivor:    group.Facts[0].ID,
				Superseded:  []string{},
			}
			if request.DryRun {
				for _, loser := range group.Facts[1:] {
					merged.Superseded = append(merged.Superseded, loser.ID)
				}
				report.Groups = append(report.Groups, merged)
				continue
			}

			now := s.clock()
			for _, loser := range group.Facts[1:] {
				superseded := loser
				superseded.Status = StatusSuperseded
				superseded.UpdatedAt = now
				if _, err := tx.SaveFact(ctx, superseded, loser.Version, now); err != nil {
					// A fact that moved on while the merge was running is left as
					// it is. Overwriting it would lose an edit somebody made on
					// purpose, and the merge can simply be asked again.
					if errors.Is(err, ErrStaleState) {
						report.Result = ResultPartial
						continue
					}
					return err
				}
				// The evidence moves only once the loser is out of force. The order
				// matters: a `continue` above has already had its way with the
				// transaction, so a move that happened first would leave a fact that
				// was supposed to be untouched standing without its evidence.
				if err := tx.MoveSources(ctx, loser.ID, merged.Survivor); err != nil {
					return err
				}
				merged.Superseded = append(merged.Superseded, loser.ID)
				report.Superseded++
			}
			if err := s.audit(ctx, tx, Audit{
				OwnerID:    request.OwnerID,
				Actor:      request.Actor,
				Action:     ActionFactMerged,
				TargetKind: TargetFact,
				TargetID:   merged.Survivor,
				Result:     report.Result,
				Detail:     mergeDetail(merged),
				CreatedAt:  now,
			}); err != nil {
				return err
			}
			report.Groups = append(report.Groups, merged)
		}
		return nil
	})
	if err != nil {
		return DedupeReport{}, err
	}
	return report, nil
}

// mergeDetail is what an audit row about a merge carries: enough to say which
// facts became one without having to read the facts, which may since have been
// purged.
func mergeDetail(group DedupeGroup) []byte {
	detail, err := json.Marshal(map[string]any{
		"fingerprint": group.Fingerprint,
		"statement":   group.Statement,
		"survivor":    group.Survivor,
		"superseded":  group.Superseded,
	})
	if err != nil {
		// A map of strings and one slice of strings cannot fail to marshal. If it
		// ever does, the audit row is still worth writing: the action happened.
		return nil
	}
	return detail
}

// audit writes one audit row through whichever repository is in hand, which is
// the transaction's whenever there is one.
func (s *Service) audit(ctx context.Context, repository Repository, row Audit) error {
	id, err := newID(auditIDPrefix)
	if err != nil {
		return err
	}
	row.ID = id
	if err := row.Validate(); err != nil {
		return err
	}
	return repository.AppendAudit(ctx, row)
}

func (q TraceQuery) validate() error {
	if q.OwnerID <= 0 {
		return fmt.Errorf("%w: the owner is %d", ErrInvalidRequest, q.OwnerID)
	}
	if q.Surface != "" && !q.Surface.Valid() {
		return fmt.Errorf("%w: %q is not a surface", ErrInvalidRequest, q.Surface)
	}
	if q.Limit < 0 {
		return fmt.Errorf("%w: a limit of %d is not a limit", ErrInvalidRequest, q.Limit)
	}
	return nil
}

func (q FactQuery) validate() error {
	if q.OwnerID <= 0 {
		return fmt.Errorf("%w: the owner is %d", ErrInvalidRequest, q.OwnerID)
	}
	if q.Surface != "" && !q.Surface.Valid() {
		return fmt.Errorf("%w: %q is not a surface", ErrInvalidRequest, q.Surface)
	}
	for _, status := range q.Statuses {
		if !status.Valid() {
			return fmt.Errorf("%w: %q is not a status", ErrInvalidRequest, status)
		}
	}
	if q.Limit < 0 {
		return fmt.Errorf("%w: a limit of %d is not a limit", ErrInvalidRequest, q.Limit)
	}
	return nil
}

func newID(prefix string) (string, error) {
	value := make([]byte, idRandomBytes)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate a %s id: %w", prefix, err)
	}
	return prefix + hex.EncodeToString(value), nil
}
