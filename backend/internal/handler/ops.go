package handler

// GET /api/v1/system/version and GET /api/v1/system/status (§2.15, SYS-01).
//
// SYS-01 asks for six things about a deployment. These two routes answer the
// read-only half of them — what this binary is, and what the process running it
// is doing — and the other two halves are refused rather than forgotten, each
// for a reason §2.15 states:
//
//   - the paths a deployment uses are a local fact. §12.2 forbids copying one
//     into anything a caller can read, and a route that answered with
//     UPLOAD_ROOT would be doing exactly that. There is no path in either view
//     below, and the case in ops_test.go checks the whole body for a slash rather
//     than a field, because the requirement is about what the answer cannot
//     carry.
//   - a writable setting is SET-04, which the configuration ruling has already
//     declined.
//
// One more thing neither view carries: a dependency's own error text. A
// driver's error string is where a DSN lives, so a readiness check that fails is
// reported as `false` and nothing else, and the error goes to the log.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"backend/internal/httpx"
	"github.com/gin-gonic/gin"
)

// Runtime is the running process as the status route reads it.
//
// Three questions and no more, which is the whole of what a status route has any
// business asking: how much work is in flight, whether the background worker is
// up, and whether the write that records model calls is losing rows. Each is a
// number or a boolean — none of them names an account, and there is no method
// here that could, because a status page is not a place to learn about anybody.
type Runtime interface {
	// ActiveTurns is how many turns are in flight right now.
	ActiveTurns() int
	// Indexing reports whether the index worker is running.
	Indexing() bool
	// UnfinishedCalls counts the model calls that have not come back (§2.7).
	UnfinishedCalls(ctx context.Context) (int64, error)
}

// OpsConfig is what the process tells these two routes about itself.
type OpsConfig struct {
	// Env is APP_ENV, which is the shape the deployment is running in.
	Env string
	// SchemaVersion is the migration this binary requires
	// (migration.LatestVersion). It is the binary's number rather than the
	// database's, and the two cannot differ while the process is up: startup
	// refuses to serve a schema that is not current.
	SchemaVersion int64
	// Readiness is the same dependency the readiness probe pings, and
	// ReadinessTimeout is the same bound it uses. A status page that disagreed
	// with /readyz would be a second answer to one question.
	Readiness        ReadinessChecker
	ReadinessTimeout time.Duration
	// Runtime is the process's own work.
	Runtime Runtime
}

// OpsHandler serves the two read-only operations routes.
type OpsHandler struct {
	build     buildView
	env       string
	readiness ReadinessChecker
	timeout   time.Duration
	runtime   Runtime
	logger    *slog.Logger
}

// NewOpsHandler constructs the operations handler.
//
// It returns an error rather than a handler that answers half a status, because
// a status route wired without the process it describes is the one kind of
// wiring mistake that cannot be seen from the outside: it would report zero
// turns and a stopped worker as calmly as it reports the truth.
func NewOpsHandler(config OpsConfig, logger *slog.Logger) (*OpsHandler, error) {
	if config.Readiness == nil {
		return nil, errors.New("ops: the status route needs the readiness check it reports")
	}
	if config.Runtime == nil {
		return nil, errors.New("ops: the status route needs the process it describes")
	}
	return &OpsHandler{
		build:     readBuild(config.SchemaVersion),
		env:       config.Env,
		readiness: config.Readiness,
		timeout:   config.ReadinessTimeout,
		runtime:   config.Runtime,
		logger:    logger,
	}, nil
}

// buildView is what this binary says about itself.
type buildView struct {
	Version   string `json:"version"`
	GoVersion string `json:"go_version"`
	Schema    int64  `json:"migration_version"`
	// BuiltAt is the moment the toolchain stamped into the binary, which for a
	// build from a checkout is the commit's time. A binary built outside one
	// carries nothing, and the field is absent rather than empty.
	BuiltAt string `json:"build_time,omitempty"`
}

// checkView is one dependency's conclusion, and it is the conclusion alone: the
// error behind a `false` stays in the log rather than in the answer.
type checkView struct {
	Name string `json:"name"`
	Ok   bool   `json:"ok"`
}

// statusView is the whole answer.
type statusView struct {
	Env string `json:"env"`
	// Checks is a list of one today — the database — and it is a list because
	// the second dependency will not be the second route: what a client draws is
	// each item's name and its conclusion.
	Checks      []checkView `json:"checks"`
	ActiveTurns int         `json:"active_turns"`
	Indexing    bool        `json:"indexing"`
	// UnfinishedCalls is a pointer because zero and "could not be read" are
	// different answers, and a client that drew one as the other would be told
	// that nothing is stuck at the moment the read stopped working.
	UnfinishedCalls *int64 `json:"unfinished_calls,omitempty"`
}

// Version answers what this binary is.
func (h *OpsHandler) Version(c *gin.Context) {
	httpx.WriteSuccess(c, http.StatusOK, h.build)
}

// Status answers what the process is doing.
func (h *OpsHandler) Status(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), h.timeout)
	defer cancel()

	checks := []checkView{{Name: "database", Ok: h.readiness.Ping(ctx) == nil}}
	status := statusView{
		Env:         h.env,
		Checks:      checks,
		ActiveTurns: h.runtime.ActiveTurns(),
		Indexing:    h.runtime.Indexing(),
	}
	if count, err := h.runtime.UnfinishedCalls(ctx); err != nil {
		// Left out rather than answered as zero: a read that did not happen must
		// not be drawn as a read that found nothing. The rest of the status is
		// still served, because the three items that do not need the database are
		// exactly what somebody reads this route for when the database is down.
		if h.logger != nil {
			h.logger.Error("counting the calls that did not come back failed",
				"request_id", httpx.RequestID(c), "error", err)
		}
	} else {
		status.UnfinishedCalls = &count
	}
	httpx.WriteSuccess(c, http.StatusOK, status)
}

// devVersion is what a binary with no version of its own says about itself.
//
// It is the toolchain's own word for a build from a source tree, and it is
// reported verbatim rather than replaced with something friendlier: an operator
// comparing this to `go version -m` should see the same string.
const devVersion = "(devel)"

// readBuild reads what the toolchain stamped into this binary.
//
// runtime/debug is the only source, and the two cases it can be in both have to
// come out as something an operator can quote. A build from a checkout has no
// module version — and a test binary has an empty one — so an empty version is
// reported as (devel) rather than as an empty string: "" reads as a field the
// server forgot to fill in, and it is the one value nobody can look up.
func readBuild(schema int64) buildView {
	view := buildView{Version: devVersion, GoVersion: runtime.Version(), Schema: schema}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return view
	}
	view.Version = normalisedVersion(info.Main.Version)
	for _, setting := range info.Settings {
		if setting.Key == "vcs.time" {
			view.BuiltAt = setting.Value
		}
	}
	return view
}

// normalisedVersion is the version a binary reports, with the empty one said out
// loud.
func normalisedVersion(version string) string {
	if strings.TrimSpace(version) == "" {
		return devVersion
	}
	return version
}
