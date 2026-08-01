// Package cli implements the ultra command-line client's commands.
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
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"

	ultrav1 "github.com/aleksclark/ultralogical/gen/go/ultra/v1"
	"github.com/aleksclark/ultralogical/gen/go/ultra/v1/ultrav1connect"
)

// Exit codes. They are part of the CLI's contract: a script distinguishes "the
// input was wrong" from "the server refused" without reading any text.
const (
	ExitOK = 0
	// ExitUsage is a malformed invocation of the CLI itself.
	ExitUsage = 2
	// ExitFailure is a typed failure returned by the API, including validation.
	ExitFailure = 1
)

// Clients bundles the generated service clients one authenticated user needs.
type Clients struct {
	Flows    ultrav1connect.FlowServiceClient
	Sessions ultrav1connect.SessionServiceClient
	Orgs     ultrav1connect.OrgServiceClient
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
	httpClient := &http.Client{
		Transport: &authTransport{token: token, base: http.DefaultTransport},
		Timeout:   60 * time.Second,
	}
	return &Clients{
		Flows:    ultrav1connect.NewFlowServiceClient(httpClient, baseURL),
		Sessions: ultrav1connect.NewSessionServiceClient(httpClient, baseURL),
		Orgs:     ultrav1connect.NewOrgServiceClient(httpClient, baseURL),
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
	url := os.Getenv("ULTRA_URL")
	if url == "" {
		url = "http://localhost:8080"
	}
	return Env{URL: url, Token: os.Getenv("ULTRA_TOKEN"), Org: os.Getenv("ULTRA_ORG")}
}

const usage = `ultra — Ultralogical command-line client

Usage:
  ultra flow validate -f FILE [--json]
  ultra flow put NAME -f FILE [--version N] [--json]
  ultra flow list [--json]
  ultra flow get NAME [--version N] [--json]
  ultra flow versions NAME [--json]
  ultra flow invoke NAME --session ID [--param k=v ...] [--params-json JSON]
                    [--version N] [--wait] [--json]
  ultra flow status INVOCATION_ID [--wait] [--json]
  ultra flow cancel INVOCATION_ID [--json]

Environment:
  ULTRA_URL    ultrad base URL (default http://localhost:8080)
  ULTRA_TOKEN  bearer token (required)
  ULTRA_ORG    default org id for org-scoped commands
`

// Run executes one CLI invocation and returns its exit code. It never calls
// os.Exit, so tests can assert the code alongside the output.
func Run(args []string, stdout, stderr io.Writer) (int, error) {
	return RunWithEnv(args, envFromOS(), stdout, stderr)
}

// RunWithEnv is Run with an explicit environment.
func RunWithEnv(args []string, environment Env, stdout, stderr io.Writer) (int, error) {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return ExitUsage, nil
	}
	switch args[0] {
	case "flow":
		return runFlow(args[1:], environment, stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return ExitOK, nil
	default:
		fmt.Fprint(stderr, usage)
		return ExitUsage, fmt.Errorf("unknown command %q", args[0])
	}
}

// NewEnv builds an environment for tests and embedders.
func NewEnv(url, token, org string) Env { return Env{URL: url, Token: token, Org: org} }

func runFlow(args []string, environment Env, stdout, stderr io.Writer) (int, error) {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return ExitUsage, nil
	}
	if environment.Token == "" {
		return ExitUsage, errors.New("ULTRA_TOKEN is required")
	}
	clients := NewClients(environment.URL, environment.Token)
	ctx := context.Background()
	sub, rest := args[0], args[1:]
	switch sub {
	case "validate":
		return flowValidate(ctx, clients, environment, rest, stdout, stderr)
	case "put":
		return flowPut(ctx, clients, environment, rest, stdout, stderr)
	case "list":
		return flowList(ctx, clients, environment, rest, stdout, stderr)
	case "get":
		return flowGet(ctx, clients, environment, rest, stdout, stderr)
	case "versions":
		return flowVersions(ctx, clients, environment, rest, stdout, stderr)
	case "invoke":
		return flowInvoke(ctx, clients, rest, stdout, stderr)
	case "status":
		return flowStatus(ctx, clients, rest, stdout, stderr)
	case "cancel":
		return flowCancel(ctx, clients, rest, stdout, stderr)
	default:
		fmt.Fprint(stderr, usage)
		return ExitUsage, fmt.Errorf("unknown flow subcommand %q", sub)
	}
}

// splitArgs separates positional arguments from flags. Go's flag package stops
// parsing at the first non-flag argument, which would make the natural
// "ultra flow put NAME -f FILE" ordering silently drop every flag. Reordering
// here means a user does not have to know that.
func splitArgs(args []string) (positional, flags []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			flags = append(flags, args[i+1:]...)
			return positional, flags
		}
		if !strings.HasPrefix(arg, "-") {
			positional = append(positional, arg)
			continue
		}
		flags = append(flags, arg)
		// A flag written as "--name value" consumes the next argument unless
		// it is boolean; "--name=value" consumes nothing.
		if strings.Contains(arg, "=") {
			continue
		}
		name := strings.TrimLeft(arg, "-")
		if booleanFlags[name] {
			continue
		}
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return positional, flags
}

// booleanFlags names the flags that take no value, so splitArgs knows which
// following token is a value and which is a positional argument.
var booleanFlags = map[string]bool{"json": true, "wait": true, "help": true, "h": true}

// paramList collects repeated --param k=v flags.
type paramList []string

func (p *paramList) String() string { return strings.Join(*p, ",") }
func (p *paramList) Set(value string) error {
	if !strings.Contains(value, "=") {
		return errors.New("expected k=v")
	}
	*p = append(*p, value)
	return nil
}

// parseParams converts --param values into typed JSON. Numbers and booleans
// are recognized so a flow declaring them can be invoked from a shell, and
// anything else stays a string.
func parseParams(values []string, paramsJSON string) (map[string]any, error) {
	out := map[string]any{}
	if paramsJSON != "" {
		if err := json.Unmarshal([]byte(paramsJSON), &out); err != nil {
			return nil, errors.New("--params-json must be a JSON object")
		}
	}
	for _, pair := range values {
		key, value, _ := strings.Cut(pair, "=")
		if key == "" {
			return nil, errors.New("--param requires a name")
		}
		switch {
		case value == "true":
			out[key] = true
		case value == "false":
			out[key] = false
		default:
			if number, err := strconv.ParseFloat(value, 64); err == nil {
				out[key] = number
				continue
			}
			out[key] = value
		}
	}
	return out, nil
}

// cliError is the machine-readable failure the CLI prints in --json mode.
type cliError struct {
	Error  string          `json:"error"`
	Code   string          `json:"code,omitempty"`
	Fields []cliFieldError `json:"fields,omitempty"`
}

type cliFieldError struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// report renders a failed API call. Validation failures carry their field
// paths through Connect error details, so the CLI prints exactly the paths the
// server produced rather than re-deriving them.
func report(w io.Writer, asJSON bool, err error) (int, error) {
	fields := fieldErrorsFrom(err)
	code := ""
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		code = connectErr.Code().String()
	}
	if asJSON {
		payload := cliError{Error: cleanMessage(err), Code: code, Fields: fields}
		body, marshalErr := json.MarshalIndent(payload, "", "  ")
		if marshalErr != nil {
			return ExitFailure, marshalErr
		}
		fmt.Fprintln(w, string(body))
		return ExitFailure, nil
	}
	if len(fields) > 0 {
		for _, field := range fields {
			fmt.Fprintf(w, "%s: %s: %s\n", field.Path, field.Code, field.Message)
		}
		return ExitFailure, nil
	}
	fmt.Fprintln(w, cleanMessage(err))
	return ExitFailure, nil
}

func cleanMessage(err error) string {
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		return connectErr.Message()
	}
	return err.Error()
}

// fieldErrorsFrom extracts structured validation failures from either a
// Connect error's details or a ValidateFlow response.
func fieldErrorsFrom(err error) []cliFieldError {
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		return nil
	}
	var out []cliFieldError
	for _, detail := range connectErr.Details() {
		message, valueErr := detail.Value()
		if valueErr != nil {
			continue
		}
		field, ok := message.(*ultrav1.FlowFieldError)
		if !ok {
			continue
		}
		out = append(out, cliFieldError{Path: field.GetPath(), Code: field.GetCode(), Message: field.GetMessage()})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Code < out[j].Code
	})
	return out
}

func writeJSON(w io.Writer, value any) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(body))
	return err
}

func requireOrg(environment Env, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if environment.Org != "" {
		return environment.Org, nil
	}
	return "", errors.New("--org or ULTRA_ORG is required")
}

func readDefinition(path string) (string, error) {
	if path == "" {
		return "", errors.New("-f FILE is required")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func flowValidate(ctx context.Context, clients *Clients, environment Env, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("ultra flow validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	file := fs.String("f", "", "definition file")
	org := fs.String("org", "", "org id")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return ExitUsage, nil
	}
	orgID, err := requireOrg(environment, *org)
	if err != nil {
		return ExitUsage, err
	}
	definition, err := readDefinition(*file)
	if err != nil {
		return ExitUsage, err
	}
	resp, err := clients.Flows.ValidateFlow(ctx, connect.NewRequest(&ultrav1.ValidateFlowRequest{
		OrgId: orgID, DefinitionJson: definition,
	}))
	if err != nil {
		return report(stderr, *asJSON, err)
	}
	if resp.Msg.GetValid() {
		if *asJSON {
			return ExitOK, writeJSON(stdout, map[string]any{"valid": true})
		}
		fmt.Fprintln(stdout, "valid")
		return ExitOK, nil
	}
	fields := make([]cliFieldError, 0, len(resp.Msg.GetErrors()))
	for _, item := range resp.Msg.GetErrors() {
		fields = append(fields, cliFieldError{Path: item.GetPath(), Code: item.GetCode(), Message: item.GetMessage()})
	}
	// An invalid definition is a typed failure, not a successful report: a
	// script must be able to gate on the exit code alone.
	if *asJSON {
		if err := writeJSON(stdout, cliError{Error: "flow definition is invalid", Fields: fields}); err != nil {
			return ExitFailure, err
		}
		return ExitFailure, nil
	}
	for _, field := range fields {
		fmt.Fprintf(stdout, "%s: %s: %s\n", field.Path, field.Code, field.Message)
	}
	return ExitFailure, nil
}

func flowPut(ctx context.Context, clients *Clients, environment Env, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("ultra flow put", flag.ContinueOnError)
	fs.SetOutput(stderr)
	file := fs.String("f", "", "definition file")
	org := fs.String("org", "", "org id")
	version := fs.Int("version", 0, "explicit version (0 assigns the next)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	positional, flags := splitArgs(args)
	if err := fs.Parse(flags); err != nil {
		return ExitUsage, nil
	}
	if len(positional) != 1 {
		return ExitUsage, errors.New("usage: ultra flow put NAME -f FILE")
	}
	orgID, err := requireOrg(environment, *org)
	if err != nil {
		return ExitUsage, err
	}
	definition, err := readDefinition(*file)
	if err != nil {
		return ExitUsage, err
	}
	resp, err := clients.Flows.PutFlow(ctx, connect.NewRequest(&ultrav1.PutFlowRequest{
		OrgId: orgID, Name: positional[0], DefinitionJson: definition, Version: int32(*version),
	}))
	if err != nil {
		return report(stderr, *asJSON, err)
	}
	if *asJSON {
		return ExitOK, writeJSON(stdout, flowView(resp.Msg.GetFlow()))
	}
	fmt.Fprintf(stdout, "%s version %d\n", resp.Msg.GetFlow().GetName(), resp.Msg.GetFlow().GetVersion())
	return ExitOK, nil
}

type flowSummary struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version int32  `json:"version"`
}

type flowDetail struct {
	flowSummary
	DefinitionJSON string `json:"definition_json"`
}

func flowView(f *ultrav1.Flow) flowDetail {
	return flowDetail{
		flowSummary:    flowSummary{ID: f.GetId(), Name: f.GetName(), Version: f.GetVersion()},
		DefinitionJSON: f.GetDefinitionJson(),
	}
}

func flowList(ctx context.Context, clients *Clients, environment Env, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("ultra flow list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	org := fs.String("org", "", "org id")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return ExitUsage, nil
	}
	orgID, err := requireOrg(environment, *org)
	if err != nil {
		return ExitUsage, err
	}
	resp, err := clients.Flows.ListFlows(ctx, connect.NewRequest(&ultrav1.ListFlowsRequest{OrgId: orgID}))
	if err != nil {
		return report(stderr, *asJSON, err)
	}
	items := make([]flowSummary, 0, len(resp.Msg.GetFlows()))
	for _, f := range resp.Msg.GetFlows() {
		items = append(items, flowSummary{ID: f.GetId(), Name: f.GetName(), Version: f.GetVersion()})
	}
	if *asJSON {
		return ExitOK, writeJSON(stdout, map[string]any{"flows": items})
	}
	for _, item := range items {
		fmt.Fprintf(stdout, "%s\tv%d\t%s\n", item.Name, item.Version, item.ID)
	}
	return ExitOK, nil
}

func flowGet(ctx context.Context, clients *Clients, environment Env, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("ultra flow get", flag.ContinueOnError)
	fs.SetOutput(stderr)
	org := fs.String("org", "", "org id")
	version := fs.Int("version", 0, "version (0 resolves latest)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	positional, flags := splitArgs(args)
	if err := fs.Parse(flags); err != nil {
		return ExitUsage, nil
	}
	if len(positional) != 1 {
		return ExitUsage, errors.New("usage: ultra flow get NAME")
	}
	orgID, err := requireOrg(environment, *org)
	if err != nil {
		return ExitUsage, err
	}
	resp, err := clients.Flows.GetFlow(ctx, connect.NewRequest(&ultrav1.GetFlowRequest{
		OrgId: orgID, Name: positional[0], Version: int32(*version),
	}))
	if err != nil {
		return report(stderr, *asJSON, err)
	}
	if *asJSON {
		return ExitOK, writeJSON(stdout, flowView(resp.Msg.GetFlow()))
	}
	fmt.Fprintf(stdout, "%s v%d\n%s\n", resp.Msg.GetFlow().GetName(),
		resp.Msg.GetFlow().GetVersion(), resp.Msg.GetFlow().GetDefinitionJson())
	return ExitOK, nil
}

func flowVersions(ctx context.Context, clients *Clients, environment Env, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("ultra flow versions", flag.ContinueOnError)
	fs.SetOutput(stderr)
	org := fs.String("org", "", "org id")
	asJSON := fs.Bool("json", false, "machine-readable output")
	positional, flags := splitArgs(args)
	if err := fs.Parse(flags); err != nil {
		return ExitUsage, nil
	}
	if len(positional) != 1 {
		return ExitUsage, errors.New("usage: ultra flow versions NAME")
	}
	orgID, err := requireOrg(environment, *org)
	if err != nil {
		return ExitUsage, err
	}
	resp, err := clients.Flows.ListFlowVersions(ctx, connect.NewRequest(&ultrav1.ListFlowVersionsRequest{
		OrgId: orgID, Name: positional[0],
	}))
	if err != nil {
		return report(stderr, *asJSON, err)
	}
	items := make([]flowSummary, 0, len(resp.Msg.GetFlows()))
	for _, f := range resp.Msg.GetFlows() {
		items = append(items, flowSummary{ID: f.GetId(), Name: f.GetName(), Version: f.GetVersion()})
	}
	if *asJSON {
		return ExitOK, writeJSON(stdout, map[string]any{"versions": items})
	}
	for _, item := range items {
		fmt.Fprintf(stdout, "v%d\t%s\n", item.Version, item.ID)
	}
	return ExitOK, nil
}

// invocationView is the CLI's stable JSON shape for an invocation. It mirrors
// the API's own view so a script reading CLI output and a client reading the
// API agree on what an invocation is.
type invocationView struct {
	ID             string         `json:"id"`
	SessionID      string         `json:"session_id"`
	FlowName       string         `json:"flow_name"`
	FlowVersion    int32          `json:"flow_version"`
	FlowID         string         `json:"flow_id"`
	State          string         `json:"state"`
	TerminalReason string         `json:"terminal_reason,omitempty"`
	Message        string         `json:"message,omitempty"`
	ParamsJSON     string         `json:"params_json,omitempty"`
	Progress       []progressView `json:"progress,omitempty"`
	Runs           []runView      `json:"runs,omitempty"`
	Envs           []envView      `json:"envs,omitempty"`
}

type progressView struct {
	Seq    int64  `json:"seq"`
	Stage  string `json:"stage"`
	Key    string `json:"key"`
	Detail string `json:"detail,omitempty"`
}

type runView struct {
	RunID     string `json:"run_id"`
	AgentName string `json:"agent_name"`
	State     string `json:"state"`
}

type envView struct {
	EnvID   string `json:"env_id"`
	EnvName string `json:"env_name"`
	State   string `json:"state"`
}

func invocationToView(inv *ultrav1.FlowInvocation) invocationView {
	out := invocationView{
		ID: inv.GetId(), SessionID: inv.GetSessionId(), FlowName: inv.GetFlowName(),
		FlowVersion: inv.GetFlowVersion(), FlowID: inv.GetFlowId(),
		State:          invocationStateName(inv.GetState()),
		TerminalReason: inv.GetTerminalReason(), Message: inv.GetMessage(),
		ParamsJSON: inv.GetParamsJson(),
	}
	for _, entry := range inv.GetProgress() {
		out.Progress = append(out.Progress, progressView{
			Seq: entry.GetSeq(), Stage: entry.GetStage(), Key: entry.GetKey(), Detail: entry.GetDetail(),
		})
	}
	for _, run := range inv.GetRuns() {
		out.Runs = append(out.Runs, runView{
			RunID: run.GetRunId(), AgentName: run.GetAgentName(), State: runStateName(run.GetState()),
		})
	}
	for _, env := range inv.GetEnvs() {
		out.Envs = append(out.Envs, envView{
			EnvID: env.GetEnvId(), EnvName: env.GetEnvName(), State: envStateName(env.GetState()),
		})
	}
	return out
}

// invocationStateName renders the wire enum as the same lowercase word the API
// and both applications use, so CLI output can be compared to API state
// directly.
func invocationStateName(state ultrav1.FlowInvocationState) string {
	return strings.ToLower(strings.TrimPrefix(state.String(), "FLOW_INVOCATION_STATE_"))
}

func runStateName(state ultrav1.RunState) string {
	return strings.ToLower(strings.TrimPrefix(state.String(), "RUN_STATE_"))
}

func envStateName(state ultrav1.EnvState) string {
	return strings.ToLower(strings.TrimPrefix(state.String(), "ENV_STATE_"))
}

func terminalInvocation(state ultrav1.FlowInvocationState) bool {
	switch state {
	case ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_COMPLETED,
		ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_FAILED,
		ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_CANCELLED:
		return true
	}
	return false
}

func flowInvoke(ctx context.Context, clients *Clients, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("ultra flow invoke", flag.ContinueOnError)
	fs.SetOutput(stderr)
	session := fs.String("session", "", "session id")
	version := fs.Int("version", 0, "version (0 resolves latest)")
	paramsJSON := fs.String("params-json", "", "parameters as a JSON object")
	wait := fs.Bool("wait", false, "wait for the invocation to reach a terminal state")
	timeout := fs.Duration("timeout", 5*time.Minute, "how long --wait waits")
	asJSON := fs.Bool("json", false, "machine-readable output")
	var params paramList
	fs.Var(&params, "param", "parameter as k=v (repeatable)")
	positional, flags := splitArgs(args)
	if err := fs.Parse(flags); err != nil {
		return ExitUsage, nil
	}
	if len(positional) != 1 {
		return ExitUsage, errors.New("usage: ultra flow invoke NAME --session ID")
	}
	if *session == "" {
		return ExitUsage, errors.New("--session is required")
	}
	values, err := parseParams(params, *paramsJSON)
	if err != nil {
		return ExitUsage, err
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return ExitFailure, err
	}
	resp, err := clients.Flows.InvokeFlow(ctx, connect.NewRequest(&ultrav1.InvokeFlowRequest{
		SessionId: *session, Name: positional[0], Version: int32(*version), ParamsJson: string(encoded),
	}))
	if err != nil {
		return report(stderr, *asJSON, err)
	}
	inv := resp.Msg.GetInvocation()
	if *wait {
		inv, err = awaitInvocation(ctx, clients, resp.Msg.GetInvocationId(), *timeout)
		if err != nil {
			return report(stderr, *asJSON, err)
		}
	}
	if *asJSON {
		if err := writeJSON(stdout, invocationToView(inv)); err != nil {
			return ExitFailure, err
		}
	} else {
		fmt.Fprintf(stdout, "%s %s\n", inv.GetId(), invocationStateName(inv.GetState()))
	}
	// A completed invocation exits zero; a failed one does not. Waiting for an
	// outcome and then hiding it would make --wait useless in a script.
	if *wait && inv.GetState() != ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_COMPLETED {
		return ExitFailure, nil
	}
	return ExitOK, nil
}

func awaitInvocation(ctx context.Context, clients *Clients, id string, timeout time.Duration) (*ultrav1.FlowInvocation, error) {
	deadline := time.Now().Add(timeout)
	var last *ultrav1.FlowInvocation
	for time.Now().Before(deadline) {
		resp, err := clients.Flows.GetFlowInvocation(ctx, connect.NewRequest(&ultrav1.GetFlowInvocationRequest{
			InvocationId: id,
		}))
		if err != nil {
			return nil, err
		}
		last = resp.Msg.GetInvocation()
		if terminalInvocation(last.GetState()) {
			return last, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	if last != nil {
		return last, fmt.Errorf("invocation %s did not finish within %s", id, timeout)
	}
	return nil, fmt.Errorf("invocation %s did not finish within %s", id, timeout)
}

func flowStatus(ctx context.Context, clients *Clients, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("ultra flow status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	wait := fs.Bool("wait", false, "wait for a terminal state")
	timeout := fs.Duration("timeout", 5*time.Minute, "how long --wait waits")
	asJSON := fs.Bool("json", false, "machine-readable output")
	positional, flags := splitArgs(args)
	if err := fs.Parse(flags); err != nil {
		return ExitUsage, nil
	}
	if len(positional) != 1 {
		return ExitUsage, errors.New("usage: ultra flow status INVOCATION_ID")
	}
	var inv *ultrav1.FlowInvocation
	if *wait {
		waited, err := awaitInvocation(ctx, clients, positional[0], *timeout)
		if err != nil {
			return report(stderr, *asJSON, err)
		}
		inv = waited
	} else {
		resp, err := clients.Flows.GetFlowInvocation(ctx, connect.NewRequest(&ultrav1.GetFlowInvocationRequest{
			InvocationId: positional[0],
		}))
		if err != nil {
			return report(stderr, *asJSON, err)
		}
		inv = resp.Msg.GetInvocation()
	}
	if *asJSON {
		return ExitOK, writeJSON(stdout, invocationToView(inv))
	}
	fmt.Fprintf(stdout, "%s %s %s\n", inv.GetId(), invocationStateName(inv.GetState()), inv.GetTerminalReason())
	for _, entry := range inv.GetProgress() {
		fmt.Fprintf(stdout, "  %d %s %s %s\n", entry.GetSeq(), entry.GetStage(), entry.GetKey(), entry.GetDetail())
	}
	for _, env := range inv.GetEnvs() {
		fmt.Fprintf(stdout, "  env %s %s %s\n", env.GetEnvName(), envStateName(env.GetState()), env.GetEnvId())
	}
	for _, run := range inv.GetRuns() {
		fmt.Fprintf(stdout, "  run %s %s %s\n", run.GetAgentName(), runStateName(run.GetState()), run.GetRunId())
	}
	return ExitOK, nil
}

func flowCancel(ctx context.Context, clients *Clients, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("ultra flow cancel", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "machine-readable output")
	positional, flags := splitArgs(args)
	if err := fs.Parse(flags); err != nil {
		return ExitUsage, nil
	}
	if len(positional) != 1 {
		return ExitUsage, errors.New("usage: ultra flow cancel INVOCATION_ID")
	}
	resp, err := clients.Flows.CancelFlowInvocation(ctx, connect.NewRequest(&ultrav1.CancelFlowInvocationRequest{
		InvocationId: positional[0],
	}))
	if err != nil {
		return report(stderr, *asJSON, err)
	}
	inv := resp.Msg.GetInvocation()
	if *asJSON {
		return ExitOK, writeJSON(stdout, invocationToView(inv))
	}
	fmt.Fprintf(stdout, "%s %s\n", inv.GetId(), invocationStateName(inv.GetState()))
	return ExitOK, nil
}
