package turn

import (
	"strings"
	"testing"

	"backend/internal/event"
)

// stubRouter answers with whatever a test set it to, and writes down what it was
// asked.
//
// The record is the half that matters: the interesting claims about routing are
// all about when the rules are consulted and when they are not, and a test can
// only see "was not consulted" if the stub says so.
type stubRouter struct {
	decision Routing
	matched  bool
	asked    []string
}

var _ Router = (*stubRouter)(nil)

func (s *stubRouter) Route(content string) (Routing, bool) {
	s.asked = append(s.asked, content)
	return s.decision, s.matched
}

// question is a decision as the rule table would make it, spelled out rather
// than imported: the runtime is not supposed to know the rules, and neither are
// its tests.
func question() Routing {
	return Routing{
		Capability: "deep_question",
		By:         "rule",
		Reason:     "话里是要出题",
		Confidence: 0.8,
	}
}

// routedFixture is a runtime that offers two capabilities and one router.
func routedFixture(t *testing.T, router Router) *runnerFixture {
	t.Helper()
	fixture := newRunnerFixture(t, func(options *Options) { options.Router = router })
	fixture.register(t,
		answering("chat", "我在"),
		answering("deep_question", "题写好了"),
	)
	return fixture
}

func TestARoutedTurnIsCreatedAsWhatItWasRoutedTo(t *testing.T) {
	router := &stubRouter{decision: question(), matched: true}
	fixture := routedFixture(t, router)

	started := fixture.start(t, StartRequest{Content: "出 10 道题"})
	fixture.repository.awaitStatus(t, started.Turn.ID, StatusCompleted)

	// The decision is made before the turn exists, so the row itself says what
	// ran: nothing downstream has to ask again, and a client reading the turn
	// back does not have to reconstruct why it was a question turn.
	if started.Turn.Capability != "deep_question" {
		t.Errorf("the turn was stored as %q, want the capability the rule named",
			started.Turn.Capability)
	}
	if len(router.asked) != 1 || router.asked[0] != "出 10 道题" {
		t.Errorf("the router was asked %v, want this turn's own words", router.asked)
	}

	// And the stream says where it went. Read by subscribing from the beginning,
	// which is what a client that reconnected would do: the decision has to be in
	// the stream, not in a message that only whoever was connected saw.
	opening := only(t, fixture.watch(t, started.Turn.ID), event.TypeSession)
	routing, ok := opening.Metadata[event.MetaRouting].(map[string]any)
	if !ok {
		t.Fatalf("the opening event carries no routing: %+v", opening.Metadata)
	}
	for key, want := range map[string]any{
		"capability": "deep_question",
		"by":         "rule",
		"reason":     "话里是要出题",
		"confidence": 0.8,
	} {
		if routing[key] != want {
			t.Errorf("routing[%q] = %v, want %v", key, routing[key], want)
		}
	}
}

func TestATurnThatNamedACapabilityIsNotReadAgain(t *testing.T) {
	// Pre-routing fills a blank; it does not overrule an answer. The router is not
	// consulted at all, so a rule that would have matched cannot get in the way of
	// a client that asked for something else.
	router := &stubRouter{decision: question(), matched: true}
	fixture := routedFixture(t, router)

	started := fixture.start(t, StartRequest{Capability: "chat", Content: "给我出 10 道题"})
	fixture.repository.awaitStatus(t, started.Turn.ID, StatusCompleted)

	if started.Turn.Capability != "chat" {
		t.Errorf("the turn ran %q, want the capability the client named", started.Turn.Capability)
	}
	if len(router.asked) != 0 {
		t.Errorf("the router was asked %v, want it left out of a turn that named its capability",
			router.asked)
	}
	opening := only(t, fixture.watch(t, started.Turn.ID), event.TypeSession)
	if _, present := opening.Metadata[event.MetaRouting]; present {
		t.Errorf("a turn with no routing carries routing: %+v", opening.Metadata)
	}
}

func TestARuleThatNamedSomethingThisBuildDoesNotOfferFallsBack(t *testing.T) {
	// A rule table belongs to the build; what a process registers belongs to the
	// deployment. When they disagree, the request gets the answer it would have
	// got before this version, and the operator gets a line saying why — a
	// routing mistake must never surface as a turn that failed.
	router := &stubRouter{matched: true, decision: Routing{
		Capability: "questions_v2", By: "rule", Reason: "话里是要出题", Confidence: 0.8,
	}}
	fixture := routedFixture(t, router)

	started := fixture.start(t, StartRequest{Content: "出 10 道题"})
	fixture.repository.awaitStatus(t, started.Turn.ID, StatusCompleted)

	if started.Turn.Capability != "chat" {
		t.Errorf("the turn ran %q, want the default capability", started.Turn.Capability)
	}
	opening := only(t, fixture.watch(t, started.Turn.ID), event.TypeSession)
	if _, present := opening.Metadata[event.MetaRouting]; present {
		t.Errorf("a turn that fell back carries routing: %+v", opening.Metadata)
	}
	if logged := fixture.logs.String(); !strings.Contains(logged, "questions_v2") {
		t.Errorf("the fallback was silent:\n%s", logged)
	}
}

func TestATurnThatWasNotRoutedCarriesNothing(t *testing.T) {
	// The rules have nothing to say about this sentence, so the turn is the
	// default one and its opening event is the event this version has always
	// published: the session's id, and no metadata at all.
	router := &stubRouter{}
	fixture := routedFixture(t, router)

	started := fixture.start(t, StartRequest{Content: "第三章讲了什么"})
	fixture.repository.awaitStatus(t, started.Turn.ID, StatusCompleted)

	if started.Turn.Capability != "chat" {
		t.Errorf("the turn ran %q, want the default capability", started.Turn.Capability)
	}
	if len(router.asked) != 1 {
		t.Errorf("the router was asked %v, want every unnamed turn read once", router.asked)
	}
	opening := only(t, fixture.watch(t, started.Turn.ID), event.TypeSession)
	if len(opening.Metadata) != 0 {
		t.Errorf("an unrouted turn carries metadata: %+v", opening.Metadata)
	}
}

func TestWithNoRouterTheRuntimeIsTheOneBeforeIt(t *testing.T) {
	// The regression: a process assembled without a router behaves exactly as it
	// did before there was one. Same capability, same opening event, line for
	// line — a deployment that never wires a router should not be able to tell
	// that this version added one.
	fixture := newRunnerFixture(t)
	fixture.register(t, answering("chat", "我在"))

	started := fixture.start(t, StartRequest{Content: "出 10 道题"})
	fixture.repository.awaitStatus(t, started.Turn.ID, StatusCompleted)

	if started.Turn.Capability != "chat" {
		t.Errorf("the turn ran %q, want the default capability", started.Turn.Capability)
	}
	opening := only(t, fixture.watch(t, started.Turn.ID), event.TypeSession)
	if opening.Type != event.TypeSession || opening.Source != event.SourceTurnRuntime {
		t.Errorf("the opening event is %+v, want the session event the runtime publishes", opening)
	}
	if opening.Content != started.Session.ID {
		t.Errorf("the opening event says %q, want the session id", opening.Content)
	}
	if len(opening.Metadata) != 0 {
		t.Errorf("the opening event carries metadata with no router: %+v", opening.Metadata)
	}
}

func TestEveryTurnIsReadOnItsOwnWords(t *testing.T) {
	// The decision belongs to the turn, not to the conversation and not to the
	// runtime: a session that started as a general chat and then asks for
	// questions is a question turn from that point on, and the next thing said in
	// it is read again from scratch. A decision kept anywhere longer-lived would
	// be a mode the user cannot leave.
	router := &stubRouter{decision: question(), matched: true}
	fixture := routedFixture(t, router)

	first := fixture.start(t, StartRequest{Content: "出 3 道题"})
	fixture.repository.awaitStatus(t, first.Turn.ID, StatusCompleted)
	second := fixture.start(t, StartRequest{SessionID: first.Session.ID, Content: "第三章讲了什么"})
	fixture.repository.awaitStatus(t, second.Turn.ID, StatusCompleted)

	if len(router.asked) != 2 {
		t.Fatalf("the router was asked %v, want each turn's own words", router.asked)
	}
	if router.asked[1] != "第三章讲了什么" {
		t.Errorf("the second turn was read from %q, want what it said", router.asked[1])
	}
	if second.Session.ID != first.Session.ID {
		t.Errorf("the two turns are in different sessions: %q and %q",
			first.Session.ID, second.Session.ID)
	}
}
