// Package static is the worked example from docs/providers.md: the smallest
// provider that still passes the shared conformance contract unmodified.
//
// One environment is one Bezalel process in an unprivileged mount namespace
// and a private root, with the workspace bind-mounted at the declared
// workdir so concurrent environments each own that absolute path. A worker
// offers the kind when a Bezalel binary is configured.
package static

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	uc "github.com/aleksclark/ultracore"
)

// Config configures the provider. Root holds one directory per environment.
type Config struct {
	Binary string `json:"binary,omitempty"`
	Root   string `json:"root,omitempty"`
}

// Provider implements uc.ResourceProvider on local processes.
type Provider struct{ cfg Config }

type handleData struct {
	ResourceID string `json:"resource_id"`
	PID        int    `json:"pid"`
	Port       int    `json:"port"`
}

// New refuses a missing binary at construction so registration fails early.
func New(cfg Config) (*Provider, error) {
	if cfg.Binary == "" {
		return nil, errors.New("static: a Bezalel binary path is required")
	}
	if _, err := os.Stat(cfg.Binary); err != nil {
		return nil, fmt.Errorf("static: bezalel binary: %w", err)
	}
	if cfg.Root == "" {
		cfg.Root = filepath.Join(os.TempDir(), "ultra-static")
	}
	return &Provider{cfg: cfg}, nil
}

func (p *Provider) dir(id uc.ResourceID) string { return filepath.Join(p.cfg.Root, string(id)) }

// Provision starts one sandboxed Bezalel. Workspace is created first so restart reuses it.

func (p *Provider) Provision(_ context.Context, r uc.Resource, token string) (json.RawMessage, error) {
	envID := r.ID
	spec, err := uc.ParseDevEnvSpec(r.Spec)
	if err != nil {
		return nil, err
	}
	workdir := spec.Workdir
	if workdir == "" {
		workdir = "/work"
	}
	workspace := filepath.Join(p.dir(envID), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return nil, fmt.Errorf("static: create workspace: %w", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("static: reserve port: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	logPath := filepath.Join(p.dir(envID), "sandbox.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("static: create sandbox log: %w", err)
	}
	// --pid is required for a private /proc; without it mount -t proc is denied.
	// Device nodes are bind-mounted individually: --rbind /dev hangs on hardened runners.
	cmd := exec.Command("unshare", "--map-root-user", "--mount", "--pid", "--fork",
		"/bin/sh", "-c", sandboxScript(p.cfg.Binary, p.dir(envID), workspace, workdir, port))
	cmd.Env = append(os.Environ(), "BEZALEL_AUTH_TOKEN="+token)
	for k, v := range spec.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("static: start sandbox: %w", err)
	}
	go func() { _ = cmd.Wait(); _ = logFile.Close() }()
	handle, err := encode(handleData{ResourceID: string(envID), PID: cmd.Process.Pid, Port: port})
	if err != nil {
		stop(handle)
		return nil, err
	}
	if err := awaitReady(port, logPath, cmd.Process.Pid); err != nil {
		stop(handle)
		return nil, err
	}
	return handle, nil
}

func sandboxScript(binary, dir, workspace, workdir string, port int) string {
	r := filepath.Join(dir, "root")
	return fmt.Sprintf(`set -e
mkdir -p %[1]s && mount -t tmpfs -o size=64m tmpfs %[1]s
mkdir -p %[1]s/usr %[1]s/proc %[1]s/dev %[1]s/tmp %[1]s/etc %[1]s/opt %[1]s%[3]s
mount --bind /usr %[1]s/usr && mount -t proc proc %[1]s/proc
touch %[1]s/dev/null %[1]s/dev/zero %[1]s/dev/urandom
mount --bind /dev/null %[1]s/dev/null && mount --bind /dev/zero %[1]s/dev/zero && mount --bind /dev/urandom %[1]s/dev/urandom
mount --bind %[2]s %[1]s%[3]s && cp %[4]s %[1]s/opt/bezalel
ln -s usr/bin %[1]s/bin && ln -s usr/lib %[1]s/lib && ln -s usr/lib64 %[1]s/lib64 2>/dev/null || true
ln -s usr/sbin %[1]s/sbin 2>/dev/null || true
# chroot lives in /usr/sbin on Ubuntu runners; PATH after env -i would miss it.
exec env -i PATH=/usr/bin:/bin:/usr/sbin HOME=%[3]s BEZALEL_AUTH_TOKEN="$BEZALEL_AUTH_TOKEN" \
  /usr/sbin/chroot %[1]s /opt/bezalel --workdir %[3]s --port %[5]d --host 127.0.0.1
`, r, workspace, workdir, binary, port)
}

func awaitReady(port int, logPath string, pid int) error {
	deadline := time.Now().Add(15 * time.Second)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return fmt.Errorf("static: sandbox exited: %s", tailLog(logPath))
		}
		if c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond); err == nil {
			_ = c.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("static: sandbox never ready: %s", tailLog(logPath))
}

func tailLog(path string) string {
	body, err := os.ReadFile(path)
	if err != nil || len(body) == 0 {
		return "(no sandbox log)"
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 512 {
		return s[len(s)-512:]
	}
	return s
}

// Status reports the sandbox's real condition. A gone process is failed.
func (p *Provider) Status(_ context.Context, r uc.Resource) (uc.ResourceStatus, error) {
	h := r.Handle
	d, err := decode(h)
	if err != nil {
		return uc.ResourceStatus{}, err
	}
	if syscall.Kill(d.PID, 0) != nil {
		return uc.ResourceStatus{State: uc.ResourceFailed, Message: "gone: " + tailLog(filepath.Join(p.dir(uc.ResourceID(d.ResourceID)), "sandbox.log"))}, nil
	}
	return uc.ResourceStatus{State: uc.ResourceReady}, nil
}

// Endpoint returns the tool endpoint the sandbox publishes.
func (p *Provider) Endpoint(_ context.Context, r uc.Resource) (uc.ToolEndpoint, error) {
	h := r.Handle
	d, err := decode(h)
	if err != nil {
		return "", err
	}
	return uc.ToolEndpoint(fmt.Sprintf("http://127.0.0.1:%d/mcp", d.Port)), nil
}

// Restart replaces the process with one carrying the rotated token.
func (p *Provider) Restart(ctx context.Context, r uc.Resource, token string) (json.RawMessage, error) {
	h := r.Handle
	stop(h)
	return p.Provision(ctx, r, token)
}

// Terminate stops the sandbox and removes its state, idempotently.
func (p *Provider) Terminate(_ context.Context, r uc.Resource) error {
	h := r.Handle
	d, err := decode(h)
	if err != nil {
		return nil
	}
	stop(h)
	if err := os.RemoveAll(p.dir(uc.ResourceID(d.ResourceID))); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("static: remove environment state: %w", err)
	}
	return nil
}

func stop(h json.RawMessage) {
	if d, err := decode(h); err == nil {
		_ = syscall.Kill(-d.PID, syscall.SIGKILL)
		_ = syscall.Kill(d.PID, syscall.SIGKILL)
	}
}

// Resources implements uc.ResourceLister.
// Probe refuses a host without unprivileged user namespaces.
func (p *Provider) Probe(context.Context) (uc.ProviderCapabilities, error) {
	caps := uc.ProviderCapabilities{
		Kind:  uc.ProviderKindStatic,
		Notes: map[uc.ProviderCapability]string{},
	}
	if err := exec.Command("unshare", "--map-root-user", "--mount", "true").Run(); err != nil {
		return caps, fmt.Errorf("static: this host has no unprivileged user namespaces: %w", err)
	}
	caps.Supported = []uc.ProviderCapability{
		uc.CapabilityEnumeratesResources, uc.CapabilityRestartPreservesState, uc.CapabilityServesToolEndpoint,
	}
	return caps, nil
}
