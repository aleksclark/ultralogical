// Package cli implements the core command-line client's commands.
//
// It exists as a package rather than living in main so the commands are
// testable against the real stack: the tests drive exactly the code path a user
// gets, including argument parsing, output rendering, and exit codes.
//
// Every command goes through the generated Connect client. There is no store
// import here, and there must never be one: a CLI that could read Postgres
// would stop being evidence that the public API is complete.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"

	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
	"github.com/aleksclark/ultracore/gen/go/core/v1/corev1connect"
)

// Exit codes. They are part of the CLI's contract: a script distinguishes "the
// input was wrong" from "the server refused" without reading any text.
const (
	ExitOK      = 0
	ExitUsage   = 2
	ExitAPI     = 3
	ExitRefused = 4
)

// Clients bundles the generated service clients one authenticated user needs.
type Clients struct {
	Orgs     corev1connect.OrgServiceClient
	Sessions corev1connect.SessionServiceClient
	Envs     corev1connect.EnvServiceClient
	HTTP     *http.Client
}

type authTransport struct {
	token string
	base  http.RoundTripper
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(req)
}

// NewClients builds API clients for a base URL and bearer token.
func NewClients(baseURL, token string) *Clients {
	httpClient := &http.Client{Timeout: 30 * time.Second, Transport: &authTransport{token: token, base: http.DefaultTransport}}
	return &Clients{
		Orgs:     corev1connect.NewOrgServiceClient(httpClient, baseURL),
		Sessions: corev1connect.NewSessionServiceClient(httpClient, baseURL),
		Envs:     corev1connect.NewEnvServiceClient(httpClient, baseURL),
		HTTP:     httpClient,
	}
}

// Env is the process environment the CLI reads. Tests set it explicitly so a
// developer's shell cannot change what a test exercises.
type Env struct {
	URL   string
	Token string
	Org   string
}

func envFromOS() Env {
	return Env{
		URL:   envOr("CORE_URL", "http://localhost:8080"),
		Token: os.Getenv("CORE_TOKEN"),
		Org:   os.Getenv("CORE_ORG"),
	}
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

const usage = `core — durable-session substrate CLI

Usage:
  core provider register NAME --kind KIND --config JSON [--json]
  core provider list [--json]
  core provider show NAME [--json]
  core provider remove NAME [--json]
  core help

Environment:
  CORE_URL    API base URL (default http://localhost:8080)
  CORE_TOKEN  bearer token (required)
  CORE_ORG    default org id for org-scoped commands
`

// Run executes one CLI invocation and returns its exit code. It never calls
// os.Exit, so tests can assert the code alongside the output.
func Run(args []string, stdout, stderr io.Writer) (int, error) {
	return RunWithEnv(args, envFromOS(), stdout, stderr)
}

// RunWithEnv is Run with an explicit environment.
func RunWithEnv(args []string, environment Env, stdout, stderr io.Writer) (int, error) {
	if len(args) == 0 {
		sink := newOut(stderr)
		sink.printf("%s", usage)
		return ExitUsage, sink.Err()
	}
	switch args[0] {
	case "provider":
		return runProvider(args[1:], environment, stdout, stderr)
	case "help", "-h", "--help":
		sink := newOut(stdout)
		sink.printf("%s", usage)
		return ExitOK, sink.Err()
	default:
		newOut(stderr).printf("%s", usage)
		return ExitUsage, fmt.Errorf("unknown command %q", args[0])
	}
}

// NewEnv builds an environment for tests and embedders.
func NewEnv(url, token, org string) Env { return Env{URL: url, Token: token, Org: org} }

// splitArgs separates positional arguments from flags.
func splitArgs(args []string) (positional, flags []string) {
	i := 0
	for i < len(args) {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				i++
				continue
			}
			if booleanFlags[name] {
				i++
				continue
			}
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				flags = append(flags, args[i+1])
				i += 2
				continue
			}
			i++
			continue
		}
		positional = append(positional, a)
		i++
	}
	return positional, flags
}

var booleanFlags = map[string]bool{
	"json": true,
	"h":    true,
	"help": true,
}

type out struct {
	w   io.Writer
	err error
}

func newOut(w io.Writer) *out { return &out{w: w} }

func (o *out) printf(format string, args ...any) {
	if o.err != nil {
		return
	}
	_, o.err = fmt.Fprintf(o.w, format, args...)
}

func (o *out) line(text string) { o.printf("%s\n", text) }

func (o *out) Err() error { return o.err }

func (o *out) writeJSON(value any) error {
	enc := json.NewEncoder(o.w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		return err
	}
	return o.err
}

func writeJSON(w io.Writer, value any) error { return newOut(w).writeJSON(value) }

func requireOrg(environment Env, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if environment.Org != "" {
		return environment.Org, nil
	}
	return "", errors.New("org id is required (pass --org or set CORE_ORG)")
}

// cliError is the machine-readable failure the CLI prints in --json mode.
type cliError struct {
	Error  string          `json:"error"`
	Fields []cliFieldError `json:"fields,omitempty"`
}

type cliFieldError struct {
	Path    string `json:"path,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

func report(w io.Writer, asJSON bool, err error) (int, error) {
	msg := cleanMessage(err)
	code := ExitAPI
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		switch connectErr.Code() {
		case connect.CodeInvalidArgument, connect.CodeNotFound, connect.CodeAlreadyExists,
			connect.CodePermissionDenied, connect.CodeFailedPrecondition:
			code = ExitRefused
		}
	}
	if asJSON {
		return code, writeJSON(w, cliError{Error: msg})
	}
	sink := newOut(w)
	sink.line(msg)
	return code, sink.Err()
}

func cleanMessage(err error) string {
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		return connectErr.Message()
	}
	return err.Error()
}

// silence unused import guard for context used by provider.go
var _ = context.Background
var _ = corev1.Org{}
