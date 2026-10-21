// Package masterypath is the mastery path as the model sees it: the capability
// that runs a mastery turn, the prompt that tells the model how to work a path,
// and the bridge the nine `mastery_*` tools call through.
//
// It is a package of its own rather than part of `internal/mastery`, and the
// reason is a dependency that cannot be reversed rather than a preference. The
// nine tools live in `internal/tool` (they share save_question's schema and its
// result shapes, which is where §2.5 says they belong), they speak mastery's
// vocabulary, so `tool` imports `mastery`. A capability in `mastery` would need
// `capability`, which reaches `tool` through the loop that runs it, and the
// three edges together are a cycle. The domain therefore stays a domain, and the
// surface — capability, prompt, tool bridge — is here. §0.3 records this.
package masterypath

import (
	"context"

	"backend/internal/capability"
	"backend/internal/tool"
)

// The mastery path as a capability: one stage, nine tools.
//
// The stage is single and that is the design's answer, not a shortcut (§2.5,
// PRD §10.2): what a mastery turn is doing — reading the tree, asking a
// question, judging an answer, building the map — is held by the *tools*, and a
// turn walks them in whatever order the learner's next sentence calls for. A
// second stage would be a second place for that state to live, and the two would
// disagree the first time a learner changed their mind.
//
// The capability itself therefore holds no path and no state. It cannot: a
// capability is one per process, and a path, a lease and an actor are one per
// turn. Everything the turn knows arrives in the context the runtime hands over,
// which is why the tools find their path there (tool.MasteryPath) and why the
// runtime — not this package — is what puts it there.

// Name is the capability's identifier: what a request asks for, what its events
// are published under, and the key its tuning is configured by.
const Name = "mastery_path"

// StageResponding is the one stage this capability walks.
//
// The name is the general assistant's, and that is deliberate rather than
// convenient: the per-stage token limits and the sampling are configured against
// stage names, and a capability that invented a name for "answer the user" would
// need its own entry in the deployment's tuning to say the same thing. PRD §10.2
// names this stage for both of V4's new capabilities.
const StageResponding = "responding"

// Manifest returns what the runtime registers for this capability.
//
// The nine tools are listed in the order a turn tends to reach for them: read
// the state, ask, judge, skip, assess, map, edit, switch, leave. The order is
// not a rule — nothing enforces it — and it is here because a manifest that
// lists tools in an arbitrary order is a manifest whose reader cannot tell
// whether the order means something.
//
// There are nine. §2.5's heading says eight and its table lists nine; the
// design's own prose settles it ("原文九个词……我们按九个工具实现"). The ninth is
// not a formality and the runtime is not a substitute for it: mastery_leave lets
// a turn that is still running put the path down — before a switch, or when it
// is done with the path and still has other work — while the runtime releases
// whatever is still held when the turn ends. Those are different moments, and
// only the model knows the first one.
func Manifest() capability.Manifest {
	return capability.Manifest{
		Name:        Name,
		Description: "掌握路径：把一门要学的东西拆成知识点，出题判卷、按证据推进每条的掌握档位",
		Stages:      []string{StageResponding},
		Tools: []string{
			tool.MasteryStateName,
			tool.MasteryQuestionName,
			tool.MasteryGradeName,
			tool.MasterySkipName,
			tool.MasteryAssessName,
			tool.MasteryMapName,
			tool.MasteryEditName,
			tool.MasterySwitchName,
			tool.MasteryLeaveName,
		},
	}
}

// Capability runs mastery turns.
type Capability struct {
	settings capability.Settings
}

var _ capability.Capability = (*Capability)(nil)

// New returns the capability for the given tuning.
func New(settings capability.Settings) (*Capability, error) {
	if err := settings.Validate(Manifest()); err != nil {
		return nil, err
	}
	return &Capability{settings: settings}, nil
}

// Manifest implements capability.Capability.
func (c *Capability) Manifest() capability.Manifest { return Manifest() }

// Run plays a turn out: one answering call, with the path read through its own
// tool rather than handed over here.
//
// The state is deliberately not fetched and attached. It is the model's first
// move to read it (mastery_state), and it has to make that move anyway — the
// knowledge point ids it needs for every call after it only exist in that
// answer. Fetching it here as well would pay for the same read twice and would
// put a possibly-stale copy of the tree in front of the model, which is worse
// than not having one: a turn that read the levels from a system message could
// argue about a level the evidence had already moved.
//
// There is no grounding call either. This capability reads no material: a
// mastery turn's material is the path, and the path is reached through tools.
func (c *Capability) Run(ctx context.Context, rc capability.RunContext) error {
	return capability.Answer(ctx, rc, c.settings, StageResponding,
		capability.Messages(systemPrompt, rc.Messages(),
			capability.PersonaMessage(rc.Persona())),
		capability.NudgeWithoutToolCall(noToolCallNudge))
}

// noToolCallNudge is what a mastery turn hears when it tries to finish without
// calling a single tool.
//
// The failure it exists for is not a refusal but a hallucination: the model
// writes "我先把题存进题库。题目已经存好了" and then asks the learner to answer,
// and no tool call was ever made — so nothing is stored, and the popup the
// learner expects has no question to show. Instructions at the top of the
// conversation did not stop it, twice. This one arrives at the end of the
// conversation, after the discarded attempt, and the loop makes it the only way
// to end the turn: a mastery answer that never touched a tool is not published.
const noToolCallNudge = `这一轮的回复没有调用任何工具，已经作废。掌握路径上的每件事都只能通过工具发生：` +
	`要出题，必须调用 mastery_question 把题真正写进题库——正文里写「题目已经存进题库」不算存进去，用户也看不到作答框；` +
	`要判卷用 mastery_grade；只是想知道现在有什么，也先调 mastery_state。` +
	`现在重新回答这一轮：先调用需要的工具，再写讲解；不要在正文里出题，也不要声称工具做过什么。`
