// Command turnprobe drives one conversation against a running server.
//
// It is a client, not a shortcut into the server: it connects over the wire and
// prints the frames it receives, so what it shows is what a real client would
// have to work with. That makes it the cheap stand-in for a user interface when
// checking that a turn really runs end to end — it does not need the LLM key,
// only a server that has one.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"backend/internal/config"
)

func main() {
	options, err := parseFlags(os.Args[1:])
	if err != nil {
		// For -h the flags package has already printed the usage.
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "turnprobe:", err)
		}
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, options); err != nil {
		log.Fatal("turnprobe: ", err)
	}
}

// options is one probe run.
type options struct {
	base       string
	token      string
	username   string
	password   string
	session    string
	capability string
	prompt     string
	reply      string
	// protocolVersion is the version to speak. It is a setting rather than a
	// constant because speaking the wrong one is one of the things worth
	// checking: the server has to refuse it.
	protocolVersion string
	// cancelAfter is how many events to watch before cancelling the turn, or
	// zero to let it finish.
	cancelAfter int
	// dropAfter is how many events to watch before dropping the connection and
	// resuming from where it got to, or zero never to drop.
	dropAfter int
	timeout   time.Duration
}

// parseFlags reads one run's options from the command line.
func parseFlags(args []string) (options, error) {
	var options options
	flags := flag.NewFlagSet("turnprobe", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.StringVar(&options.base, "base", "", "base HTTP URL of the server (default: from HTTP_ADDR in .env)")
	flags.StringVar(&options.token, "token", "", "bearer token to connect with")
	flags.StringVar(&options.username, "username", "", "username to log in with when no token is given")
	flags.StringVar(&options.password, "password", "", "password to log in with when no token is given")
	flags.StringVar(&options.session, "session", "", "an existing session to continue (default: create one)")
	flags.StringVar(&options.capability, "capability", "", "capability to run (default: the server's)")
	flags.StringVar(&options.prompt, "prompt", "用一句话解释闭包。", "what to say")
	flags.StringVar(&options.reply, "reply", "", "answer to give when the turn asks a question")
	flags.StringVar(&options.protocolVersion, "protocol-version", defaultProtocolVersion,
		"the protocol version to speak, to check that the server refuses another one")
	flags.IntVar(&options.cancelAfter, "cancel-after", 0, "cancel the turn after this many events")
	flags.IntVar(&options.dropAfter, "drop-after", 0, "drop the connection after this many events and resume")
	flags.DurationVar(&options.timeout, "timeout", 3*time.Minute, "how long the whole probe may take")
	if err := flags.Parse(args); err != nil {
		return options, err
	}

	if options.token == "" && options.username == "" {
		return options, errors.New("give either -token or -username with -password")
	}
	if options.cancelAfter < 0 || options.dropAfter < 0 {
		return options, errors.New("-cancel-after and -drop-after cannot be negative")
	}
	if options.cancelAfter > 0 && options.dropAfter > 0 {
		return options, errors.New("-cancel-after and -drop-after are two different probes; run them one at a time")
	}
	return options, nil
}

func run(ctx context.Context, options options) error {
	ctx, cancel := context.WithTimeout(ctx, options.timeout)
	defer cancel()

	if options.base == "" {
		// Configuration lives in .env, so where the server listens comes from
		// there too rather than from a second default written here.
		cfg, err := config.Load(".env")
		if err != nil {
			return fmt.Errorf("load configuration: %w", err)
		}
		options.base = localBase(cfg.HTTPAddr)
	}

	client := &httpClient{base: strings.TrimSuffix(options.base, "/"), http: &http.Client{Timeout: 30 * time.Second}}

	token := options.token
	if token == "" {
		issued, err := client.login(ctx, options.username, options.password)
		if err != nil {
			return err
		}
		token = issued
		fmt.Println("logged in")
	}

	sessionID := options.session
	if sessionID == "" {
		created, err := client.createSession(ctx, token)
		if err != nil {
			return err
		}
		sessionID = created
	}
	fmt.Println("session:", sessionID)

	return converse(ctx, options, token, sessionID)
}

// localBase turns a listen address into a URL to connect to.
//
// An address of ":8080" listens on every interface, which is not an address to
// dial; loopback is the same process from the outside, and is what a probe on
// the same machine means.
func localBase(addr string) string {
	if host, port, found := strings.Cut(addr, ":"); found {
		if host == "" {
			host = "127.0.0.1"
		}
		return "http://" + host + ":" + port
	}
	return "http://" + addr
}
