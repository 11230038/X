package handler

// The cases for GET /api/v1/system/version and GET /api/v1/system/status.
//
// §2.15 asks for two things of these answers and both are absences: no physical
// path, and no dependency's error text. The two cases about that read the whole
// body rather than a field — the status answer is checked for a slash, because
// every path this deployment knows is a Windows or POSIX one and both contain
// one — and the case about a failing check asserts that the address the driver
// complained about is nowhere in the answer.
//
// The version's own rule is the other half: a build with no version says
// "(devel)" and never "". That is checked twice — once on the pure function, for
// the input a test binary has, and once on the wire, which is where a handler
// that hardcoded something else would show up.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// opsReadyStub is the dependency the status route reports on. What it returns is
// never shown; whether it returned an error is.
type opsReadyStub struct{ err error }

func (s opsReadyStub) Ping(context.Context) error { return s.err }

// opsRuntimeStub is the process as the status route reads it.
type opsRuntimeStub struct {
	turns    int
	indexing bool
	calls    int64
	err      error
}

func (s opsRuntimeStub) ActiveTurns() int { return s.turns }
func (s opsRuntimeStub) Indexing() bool   { return s.indexing }
func (s opsRuntimeStub) UnfinishedCalls(context.Context) (int64, error) {
	return s.calls, s.err
}

const opsSchema = int64(17)

func newOpsRouter(t *testing.T, config OpsConfig) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	if config.Readiness == nil {
		config.Readiness = opsReadyStub{}
	}
	if config.Runtime == nil {
		config.Runtime = opsRuntimeStub{}
	}
	handler, err := NewOpsHandler(config, testHandlerLogger())
	if err != nil {
		t.Fatalf("NewOpsHandler() error = %v", err)
	}
	router := gin.New()
	router.GET("/api/v1/system/version", handler.Version)
	router.GET("/api/v1/system/status", handler.Status)
	return router
}

func TestOpsVersionAnswersWhatTheBinaryIs(t *testing.T) {
	router := newOpsRouter(t, OpsConfig{Env: "development", SchemaVersion: opsSchema})
	recorder := call(t, router, http.MethodGet, "/api/v1/system/version", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}

	var body struct {
		Data struct {
			Version   string `json:"version"`
			GoVersion string `json:"go_version"`
			Schema    int64  `json:"migration_version"`
			BuiltAt   string `json:"build_time"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &body)

	// The rule, said as a condition rather than as a string: a version is never
	// empty, and the one case where the toolchain has nothing to say is the case
	// a test binary is in.
	if body.Data.Version == "" {
		t.Error("version is empty: the one answer nobody can look up")
	}
	if info, ok := debug.ReadBuildInfo(); ok && strings.TrimSpace(info.Main.Version) == "" {
		if body.Data.Version != devVersion {
			t.Errorf("version = %q, want %q for a build with no version of its own",
				body.Data.Version, devVersion)
		}
	}
	if body.Data.GoVersion != runtime.Version() {
		t.Errorf("go_version = %q, want the toolchain this process runs on, %q",
			body.Data.GoVersion, runtime.Version())
	}
	if body.Data.Schema != opsSchema {
		t.Errorf("migration_version = %d, want the schema this binary requires, %d",
			body.Data.Schema, opsSchema)
	}
	// A build from a checkout has a commit time and one from an archive has
	// nothing; neither is a slash, and neither is this route's business to invent.
	if body.Data.BuiltAt != "" && !strings.Contains(body.Data.BuiltAt, "T") {
		t.Errorf("build_time = %q, want a timestamp or nothing", body.Data.BuiltAt)
	}
}

// The version's two inputs, both of which a build can actually be in.
func TestTheVersionIsNeverTheEmptyString(t *testing.T) {
	for _, tt := range []struct {
		name  string
		given string
		want  string
	}{
		{name: "no version at all", given: "", want: devVersion},
		{name: "nothing but space", given: "   ", want: devVersion},
		{name: "the toolchain's own word", given: devVersion, want: devVersion},
		{name: "a released version", given: "v1.2.3", want: "v1.2.3"},
		{name: "a pseudo-version", given: "v1.2.4-0.20261012-abcdef123456", want: "v1.2.4-0.20261012-abcdef123456"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalisedVersion(tt.given); got != tt.want {
				t.Errorf("normalisedVersion(%q) = %q, want %q", tt.given, got, tt.want)
			}
		})
	}
}

func TestOpsStatusAnswersWhatTheProcessIsDoing(t *testing.T) {
	router := newOpsRouter(t, OpsConfig{
		Env: "development", SchemaVersion: opsSchema,
		Runtime: opsRuntimeStub{turns: 2, indexing: true, calls: 5},
	})
	recorder := call(t, router, http.MethodGet, "/api/v1/system/status", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}

	var body struct {
		Data struct {
			Env    string `json:"env"`
			Checks []struct {
				Name string `json:"name"`
				Ok   bool   `json:"ok"`
			} `json:"checks"`
			ActiveTurns     int   `json:"active_turns"`
			Indexing        bool  `json:"indexing"`
			UnfinishedCalls int64 `json:"unfinished_calls"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &body)

	if body.Data.Env != "development" {
		t.Errorf("env = %q, want the mode the deployment runs in", body.Data.Env)
	}
	if len(body.Data.Checks) != 1 || body.Data.Checks[0].Name != "database" || !body.Data.Checks[0].Ok {
		t.Errorf("checks = %+v, want the database concluded good", body.Data.Checks)
	}
	if body.Data.ActiveTurns != 2 || !body.Data.Indexing {
		t.Errorf("the process's own work is %d turns / indexing=%v, want 2 / true",
			body.Data.ActiveTurns, body.Data.Indexing)
	}
	if body.Data.UnfinishedCalls != 5 {
		t.Errorf("unfinished_calls = %d, want the count the runtime reported", body.Data.UnfinishedCalls)
	}
}

// The status is still an answer when a dependency is down: the three items that
// do not need the database are what somebody reads this route for at that
// moment. What must not appear is the driver's own complaint, which is where an
// address and a DSN live.
func TestOpsStatusReportsAFailingCheckWithoutItsError(t *testing.T) {
	router := newOpsRouter(t, OpsConfig{
		Env: "development", SchemaVersion: opsSchema,
		Readiness: opsReadyStub{err: errors.New("dial tcp 127.0.0.1:2881: connect: connection refused")},
	})
	recorder := call(t, router, http.MethodGet, "/api/v1/system/status", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}

	var body struct {
		Data struct {
			Checks []struct {
				Ok bool `json:"ok"`
			} `json:"checks"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &body)
	if len(body.Data.Checks) != 1 || body.Data.Checks[0].Ok {
		t.Fatalf("checks = %+v, want the database concluded bad", body.Data.Checks)
	}
	answer := recorder.Body.String()
	for _, forbidden := range []string{"2881", "127.0.0.1", "refused", "dial"} {
		if strings.Contains(answer, forbidden) {
			t.Errorf("the answer carries %q, which is the dependency's own words: %s", forbidden, answer)
		}
	}
}

// A number that could not be read is left out rather than answered as zero, and
// the rest of the status is still served.
func TestOpsStatusLeavesOutACountItCouldNotRead(t *testing.T) {
	router := newOpsRouter(t, OpsConfig{
		Env: "development", SchemaVersion: opsSchema,
		Runtime: opsRuntimeStub{turns: 1, indexing: true, err: errors.New("llm_calls: the table is missing")},
	})
	recorder := call(t, router, http.MethodGet, "/api/v1/system/status", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}

	var body struct {
		Data struct {
			ActiveTurns     int    `json:"active_turns"`
			UnfinishedCalls *int64 `json:"unfinished_calls"`
			Indexing        bool   `json:"indexing"`
		} `json:"data"`
	}
	decodeBody(t, recorder, &body)
	if body.Data.UnfinishedCalls != nil {
		t.Errorf("unfinished_calls = %v, want it absent: the read did not happen", *body.Data.UnfinishedCalls)
	}
	if body.Data.ActiveTurns != 1 || !body.Data.Indexing {
		t.Errorf("the rest of the status was not served: %+v", body.Data)
	}
	if strings.Contains(recorder.Body.String(), "llm_calls") {
		t.Errorf("the answer carries the failure behind the missing count: %s", recorder.Body.String())
	}
}

// §2.15's other absence, and the reason the check is on the whole body: a
// physical path is a slash, so an answer with no slash in it has no path in it.
// /healthz and /readyz stay as they were — this is the route that grew.
func TestOpsAnswersCarryNoPhysicalPath(t *testing.T) {
	router := newOpsRouter(t, OpsConfig{
		Env: "development", SchemaVersion: opsSchema,
		Runtime: opsRuntimeStub{turns: 1, indexing: true, calls: 2},
	})
	for _, path := range []string{"/api/v1/system/version", "/api/v1/system/status"} {
		t.Run(path, func(t *testing.T) {
			recorder := call(t, router, http.MethodGet, path, "")
			answer := recorder.Body.String()
			if strings.Contains(answer, "/") {
				t.Errorf("the answer carries a slash, which is what every path has: %s", answer)
			}
			var envelope map[string]json.RawMessage
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode %q: %v", answer, err)
			}
		})
	}
}

// A status route wired without the process it describes is the one wiring
// mistake that cannot be seen from the outside: it would report zero turns and a
// stopped worker as calmly as it reports the truth.
func TestOpsRefusesToBeBuiltWithoutWhatItReports(t *testing.T) {
	for _, tt := range []struct {
		name   string
		config OpsConfig
	}{
		{name: "no readiness check", config: OpsConfig{Runtime: opsRuntimeStub{}}},
		{name: "no runtime", config: OpsConfig{Readiness: opsReadyStub{}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			handler, err := NewOpsHandler(tt.config, testHandlerLogger())
			if err == nil {
				t.Fatalf("NewOpsHandler(%+v) was accepted", tt.config)
			}
			if handler != nil {
				t.Error("a handler was returned alongside the failure")
			}
		})
	}
}
