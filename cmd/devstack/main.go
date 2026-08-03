// devstack supports the one-command local development stack.
//
// Subcommands:
//
//	seed   Create the dev org, user, membership, local provider instance, and
//	       an inference credential pointing at a local model endpoint. Prints
//	       JSON describing what it created.
//	smoke  Drive the running stack through the shipped API: create a session,
//	       stream an agent run, provision a development environment, run a
//	       command in it, and terminate. Exits nonzero on any
//	       failure so the caller can fail the build.
//	model  Serve a minimal OpenAI-compatible streaming endpoint so the local
//	       stack can run agents without a vendor account.
//
// Configuration:
//
//	DATABASE_URL      Postgres connection string (seed)
//	CORE_MASTER_KEY  credential master key (seed)
//	CORE_DEV_EMAIL   dev user email (seed, default dev@example.com)
//	CORE_MODEL_URL   base URL of the local model endpoint (seed)
//	CORE_SMOKE_API   cored base URL (smoke)
//	CORE_SMOKE_TOKEN dev bearer token (smoke)
//	CORE_SMOKE_ORG   org id to work in (smoke)
//	CORE_MODEL_ADDR  listen address for the model endpoint (model)
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	uc "github.com/aleksclark/ultracore"
	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
	"github.com/aleksclark/ultracore/gen/go/core/v1/corev1connect"
	"github.com/aleksclark/ultracore/postgres"
	"github.com/aleksclark/ultracore/secrets"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: devstack seed|smoke|model")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "seed":
		err = seed(context.Background())
	case "smoke":
		err = smoke(context.Background())
	case "model":
		err = serveModel()
	default:
		err = fmt.Errorf("unknown subcommand %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "devstack %s: %v\n", os.Args[1], err)
		os.Exit(1)
	}
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// seed makes the local stack usable: an org, a user with membership, a local
// Docker provider instance named "default", and an inference credential
// pointing at the local model endpoint. It is idempotent.
func seed(ctx context.Context) error {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	keyring, err := secrets.NewAESKeyring(os.Getenv("CORE_MASTER_KEY"))
	if err != nil {
		return err
	}
	if err := postgres.Migrate(ctx, databaseURL); err != nil {
		return err
	}
	store, pool, err := postgres.Connect(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	email := envOr("CORE_DEV_EMAIL", "dev@example.com")
	user, err := store.Users().GetByEmail(ctx, email)
	if errors.Is(err, uc.ErrNotFound) {
		user = uc.User{ID: uc.UserID(uuid.NewString()), Email: email, Display: "Dev"}
		if err := store.Users().Create(ctx, user); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	orgs, err := store.Orgs().ListForUser(ctx, user.ID)
	if err != nil {
		return err
	}
	var org uc.Org
	if len(orgs) > 0 {
		org = orgs[0]
	} else {
		org = uc.Org{ID: uc.OrgID(uuid.NewString()), Name: "dev"}
		if err := store.Orgs().Create(ctx, org); err != nil {
			return err
		}
		if err := store.Orgs().AddMember(ctx, uc.OrgMember{OrgID: org.ID, UserID: user.ID, Role: uc.OrgRoleOwner}); err != nil {
			return err
		}
	}

	scope := store.Org(org.ID)
	if _, err := scope.Providers().GetByName(ctx, "default"); errors.Is(err, uc.ErrNotFound) {
		if err := scope.Providers().Create(ctx, uc.ProviderInstance{
			ID: uc.ProviderInstanceID(uuid.NewString()), OrgID: org.ID,
			Kind: uc.ProviderKindLocalDocker, Name: "default",
			State: "ready",
		}); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	if modelURL := os.Getenv("CORE_MODEL_URL"); modelURL != "" {
		payload, err := json.Marshal(uc.InferencePayload{APIKey: "dev-local-model-key", BaseURL: modelURL})
		if err != nil {
			return err
		}
		enc, err := keyring.Encrypt(payload)
		if err != nil {
			return err
		}
		if err := scope.Credentials().Put(ctx, uc.Credential{
			Kind: uc.CredentialKindOpenAI, Name: "default", EncPayload: enc,
		}); err != nil {
			return err
		}
	}

	return json.NewEncoder(os.Stdout).Encode(map[string]string{
		"org_id":  string(org.ID),
		"user_id": string(user.ID),
		"email":   email,
	})
}

type smokeClient struct {
	sessions corev1connect.SessionServiceClient
	events   corev1connect.EventServiceClient
	agents   corev1connect.AgentServiceClient
	resources     corev1connect.ResourceServiceClient
}

type bearer struct {
	token string
	base  http.RoundTripper
}

func (b *bearer) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(req)
}

// smoke exercises the shipped API end to end against the running stack. It is
// the "is the documented stack actually usable" check, not a unit test.
func smoke(ctx context.Context) error {
	api := envOr("CORE_SMOKE_API", "http://127.0.0.1:8080")
	token := envOr("CORE_SMOKE_TOKEN", "dev-token")
	org := os.Getenv("CORE_SMOKE_ORG")
	if org == "" {
		return errors.New("CORE_SMOKE_ORG is required")
	}
	httpClient := &http.Client{Transport: &bearer{token: token, base: http.DefaultTransport}, Timeout: 2 * time.Minute}
	client := smokeClient{
		sessions: corev1connect.NewSessionServiceClient(httpClient, api),
		events:   corev1connect.NewEventServiceClient(httpClient, api),
		agents:   corev1connect.NewAgentServiceClient(httpClient, api),
		resources:     corev1connect.NewResourceServiceClient(httpClient, api),
		}

	created, err := client.sessions.CreateSession(ctx, connect.NewRequest(&corev1.CreateSessionRequest{
		OrgId: org, Title: "dev stack smoke",
	}))
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	session := created.Msg.GetSession().GetId()
	fmt.Printf("smoke: session %s\n", session)

	// Stream a run and require incremental deltas: a stack that cannot stream
	// is not usable for agent work.
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	stream, err := client.events.Subscribe(streamCtx, connect.NewRequest(&corev1.SubscribeRequest{
		SessionId: session, FromSeq: 0,
	}))
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	defer func() { _ = stream.Close() }()

	run, err := client.agents.StartRun(ctx, connect.NewRequest(&corev1.StartRunRequest{
		SessionId: session, Prompt: "say hello from the dev stack",
	}))
	if err != nil {
		return fmt.Errorf("start run: %w", err)
	}
	fmt.Printf("smoke: run %s\n", run.Msg.GetRun().GetId())

	deltas, terminal := 0, ""
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) && terminal == "" {
		if !stream.Receive() {
			break
		}
		event := stream.Msg().GetEvent()
		if event == nil {
			continue
		}
		switch payload := event.GetPayload().GetPayload().(type) {
		case *corev1.EventPayload_TextDelta:
			deltas++
		case *corev1.EventPayload_RunCompleted:
			terminal = "completed"
		case *corev1.EventPayload_RunFailed:
			terminal = "failed: " + payload.RunFailed.GetReason() + " " + payload.RunFailed.GetMessage()
		case *corev1.EventPayload_RunCancelled:
			terminal = "cancelled"
		}
	}
	if terminal != "completed" {
		return fmt.Errorf("run did not complete (state %q, %d deltas)", terminal, deltas)
	}
	if deltas < 1 {
		return errors.New("run completed without streaming any text deltas")
	}
	fmt.Printf("smoke: streamed %d deltas and completed\n", deltas)

	// Provision a real environment and run a command in it.
	provisioned, err := client.resources.ProvisionResource(ctx, connect.NewRequest(&corev1.ProvisionResourceRequest{
		SessionId:        session,
		Spec:             &corev1.DevEnvSpec{Name: "smoke", Workdir: "/work"},
		ProviderInstance: "default",
	}))
	if err != nil {
		return fmt.Errorf("provision env: %w", err)
	}
	envID := provisioned.Msg.GetResource().GetId()
	ready := false
	deadline = time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		got, err := client.resources.GetResource(ctx, connect.NewRequest(&corev1.GetResourceRequest{ResourceId: envID}))
		if err == nil {
			switch got.Msg.GetResource().GetState() {
			case corev1.ResourceState_RESOURCE_STATE_READY:
				ready = true
			case corev1.ResourceState_RESOURCE_STATE_FAILED:
				return fmt.Errorf("env failed: %s", got.Msg.GetResource().GetFailureMessage())
			}
		}
		if ready {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !ready {
		return errors.New("environment never became ready")
	}
	fmt.Printf("smoke: environment %s ready\n", envID)

	exec, err := client.resources.ExecPreview(ctx, connect.NewRequest(&corev1.ExecPreviewRequest{
		ResourceId: envID, Command: "echo dev-stack-smoke",
	}))
	if err != nil {
		return fmt.Errorf("exec preview: %w", err)
	}
	if !strings.Contains(exec.Msg.GetOutput(), "dev-stack-smoke") {
		return fmt.Errorf("exec output = %q", exec.Msg.GetOutput())
	}
	fmt.Println("smoke: ran a command in the environment")

	if _, err := client.resources.TerminateResource(ctx, connect.NewRequest(&corev1.TerminateResourceRequest{ResourceId: envID})); err != nil {
		return fmt.Errorf("terminate env: %w", err)
	}
	deadline = time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		got, err := client.resources.GetResource(ctx, connect.NewRequest(&corev1.GetResourceRequest{ResourceId: envID}))
		if err == nil && got.Msg.GetResource().GetState() == corev1.ResourceState_RESOURCE_STATE_TERMINATED {
			fmt.Println("smoke: environment terminated")
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return errors.New("environment never reached terminated")
}

// serveModel is a minimal OpenAI-compatible streaming endpoint so the local
// stack can run agents without a vendor account. It streams several small
// chunks so the local UI shows real incremental rendering.
func serveModel() error {
	addr := envOr("CORE_MODEL_ADDR", "127.0.0.1:8091")
	mux := http.NewServeMux()
	handler := func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Stream bool `json:"stream"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		chunks := []string{"Hello ", "from ", "the ", "local ", "dev ", "stack."}
		if !body.Stream {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "devstack", "object": "chat.completion", "model": "devstack",
				"choices": []any{map[string]any{
					"index": 0, "finish_reason": "stop",
					"message": map[string]any{"role": "assistant", "content": strings.Join(chunks, "")},
				}},
			})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, chunk := range chunks {
			frame, _ := json.Marshal(map[string]any{
				"id": "devstack", "object": "chat.completion.chunk", "model": "devstack",
				"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": chunk}}},
			})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", frame)
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(40 * time.Millisecond)
		}
		final, _ := json.Marshal(map[string]any{
			"id": "devstack", "object": "chat.completion.chunk", "model": "devstack",
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
		})
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", final)
		if flusher != nil {
			flusher.Flush()
		}
	}
	mux.HandleFunc("POST /v1/chat/completions", handler)
	mux.HandleFunc("POST /chat/completions", handler)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	fmt.Printf("devstack model listening on %s\n", addr)
	return srv.ListenAndServe()
}
