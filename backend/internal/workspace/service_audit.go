package workspace

// The ledger's half of the service: what a job records about itself when it
// works and when it refuses.
//
// §12.4 asks the ledger to hold both, and the two are one subject here because
// they are one decision seen twice: a job that failed is recorded as a job that
// failed, which is exactly why a caller gets a job id back rather than a bare
// error. The refusal and the record of it are written by the same function, so
// there is no ordering for a caller to get wrong.
//
// It is separate from service.go because it is the only part of that file that
// knows the ledger exists. An export that recorded nothing would still make a
// package, and the four functions below are the whole of what it would lose.

import (
	"context"
	"errors"

	"backend/internal/audit"
)

// reject marks a job failed, records the failure and returns the error.
//
// The job is marked before the error is returned, and the ordering is the whole
// reason the caller gets a job id back for a failure: what went wrong is in the
// row, and the error that travels up is the account's answer rather than the
// record of it.
func (s *Service) reject(ctx context.Context, actor Actor, job Job, code string, cause error) (Job, error) {
	// The context may be the one that just ran out, so the row is written on a
	// context that has not: marking the job failed is the one write that must
	// happen even when the reason it is happening was a deadline.
	recordCtx := context.WithoutCancel(ctx)
	now := s.now().UTC()
	if err := s.repository.SaveState(recordCtx, job.ID, StateFailed, code, now); err != nil {
		return Job{}, errors.Join(cause, err)
	}
	job.State = StateFailed
	job.FailureCode = code
	job.UpdatedAt = now
	detail := audit.Detail{Counts: job.Counts}
	detail.Reason = code
	s.record(recordCtx, actor, job, audit.ResultFailed, detail)
	return job, cause
}

// record writes one audit entry about a job.
//
// A failure to record is not returned. The work has already happened, and an
// export that succeeded and then refused to say so would be a worse answer than
// one whose audit line is missing — which is the same call the library's
// download makes for the same reason.
//
// The action comes from the job rather than from the caller, because the two
// directions are two actions (§2.6) and a call site that had to remember which
// one it was is a call site that will one day record an import as an export.
func (s *Service) record(ctx context.Context, actor Actor, job Job, result audit.Result, detail audit.Detail) {
	s.recordAs(ctx, actor, job, actionOf(job), result, detail)
}

// recordAs is record under an action the job's direction does not name, which is
// the recovery and nothing else.
func (s *Service) recordAs(ctx context.Context, actor Actor, job Job, action audit.Action, result audit.Result, detail audit.Detail) {
	event := audit.Event{
		Actor:  actor.auditActor(),
		Action: action,
		Target: audit.Ref{Kind: audit.TargetWorkspaceJob, ID: job.ID},
		Result: result,
		Detail: detail,
	}
	_ = s.auditor.Record(ctx, event)
}

// actionOf is the audit action one job's direction is recorded under.
func actionOf(job Job) audit.Action {
	if job.Kind == JobImport {
		return audit.ActionWorkspaceImported
	}
	return audit.ActionWorkspaceExported
}
