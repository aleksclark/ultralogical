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

	ultra "github.com/aleksclark/ultralogical"
)

// Config configures the provider. Root holds one directory per environment.
type Config struct {
	Binary string `json:"binary,omitempty"`
	Root   string `json:"root,omitempty"`
}

// Provider implements ultra.EnvProvider on local processes.
type Provider struct{ cfg Config }

type handleData struct {
	EnvID string `json:"env_id"`
	PID   int    `json:"pid"`
	Port  int    `json:"port"`
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

func (p *Provider) dir(id ultra.EnvID) string { return filepath.Join(p.cfg.Root, string(id)) }

// Provision starts one sandboxed Bezalel. Workspace is created first so restart reuses it.
func (p *Provider) Provision(_ context.Context, envID ultra.EnvID, spec ultra.EnvSpec, token string) (ultra.ProviderHandle, error) {
	workdir := spec.Workdir
	if workdir == "" {
		workdir = "/work"
	}
	workspace := filepath.Join(p.dir(envID), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return ultra.ProviderHandle{}, fmt.Errorf("static: create workspace: %w", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return ultra.ProviderHandle{}, fmt.Errorf("static: reserve port: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	logPath := filepath.Join(p.dir(envID), "sandbox.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return ultra.ProviderHandle{}, fmt.Errorf("static: create sandbox log: %w", err)
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
		return ultra.ProviderHandle{}, fmt.Errorf("static: start sandbox: %w", err)
	}
	go func() { _ = cmd.Wait(); _ = logFile.Close() }()
	handle, err := encode(handleData{EnvID: string(envID), PID: cmd.Process.Pid, Port: port})
	if err != nil {
		stop(handle)
		return ultra.ProviderHandle{}, err
	}
	if err := awaitReady(port, logPath, cmd.Process.Pid); err != nil {
		stop(handle)
		return ultra.ProviderHandle{}, err
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
exec env -i PATH=/usr/bin:/bin HOME=%[3]s BEZALEL_AUTH_TOKEN="$BEZALEL_AUTH_TOKEN" chroot %[1]s /opt/bezalel --workdir %[3]s --port %[5]d --host 127.0.0.1
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
func (p *Provider) Status(_ context.Context, h ultra.ProviderHandle) (ultra.ProviderStatus, error) {
	d, err := decode(h)
	if err != nil {
		return ultra.ProviderStatus{}, err
	}
	if syscall.Kill(d.PID, 0) != nil {
		return ultra.ProviderStatus{State: ultra.EnvFailed, Message: "gone: " + tailLog(filepath.Join(p.dir(ultra.EnvID(d.EnvID)), "sandbox.log"))}, nil
	}
	return ultra.ProviderStatus{State: ultra.EnvReady}, nil
}

// Endpoint returns the tool endpoint the sandbox publishes.
func (p *Provider) Endpoint(_ context.Context, h ultra.ProviderHandle) (string, error) {
	d, err := decode(h)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("http://127.0.0.1:%d/mcp", d.Port), nil
}

// Restart replaces the process with one carrying the rotated token.
func (p *Provider) Restart(ctx context.Context, id ultra.EnvID, h ultra.ProviderHandle, spec ultra.EnvSpec, token string) (ultra.ProviderHandle, error) {
	stop(h)
	return p.Provision(ctx, id, spec, token)
}

// Terminate stops the sandbox and removes its state, idempotently.
func (p *Provider) Terminate(_ context.Context, h ultra.ProviderHandle) error {
	d, err := decode(h)
	if err != nil {
		return nil
	}
	stop(h)
	if err := os.RemoveAll(p.dir(ultra.EnvID(d.EnvID))); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("static: remove environment state: %w", err)
	}
	return nil
}

func stop(h ultra.ProviderHandle) {
	if d, err := decode(h); err == nil {
		_ = syscall.Kill(-d.PID, syscall.SIGKILL)
		_ = syscall.Kill(d.PID, syscall.SIGKILL)
	}
}

// Resources implements ultra.EnvResourceLister.
func (p *Provider) Resources(_ context.Context, id ultra.EnvID) ([]string, error) {
	if _, err := os.Stat(p.dir(id)); err != nil {
		return nil, nil
	}
	return []string{"sandbox/" + string(id)}, nil
}

// Probe refuses a host without unprivileged user namespaces.
func (p *Provider) Probe(context.Context) (ultra.ProviderCapabilities, error) {
	caps := ultra.ProviderCapabilities{
		Kind: ultra.ProviderKindStatic,
		Notes: map[ultra.ProviderCapability]string{
			ultra.CapabilityNamespaceIsolation: "environments share the host kernel and network namespace",
			ultra.CapabilityResourceQuota:      "the example provider enforces no ceiling",
		},
	}
	if err := exec.Command("unshare", "--map-root-user", "--mount", "true").Run(); err != nil {
		return caps, fmt.Errorf("static: this host has no unprivileged user namespaces: %w", err)
	}
	caps.Supported = []ultra.ProviderCapability{
		ultra.CapabilityEnumeratesResources, ultra.CapabilityRestartPreservesWorkspace, ultra.CapabilityServesToolEndpoint,
	}
	return caps, nil
}

func encode(d handleData) (ultra.ProviderHandle, error) {
	b, err := json.Marshal(d)
	if err != nil {
		return ultra.ProviderHandle{}, err
	}
	return ultra.ProviderHandle{Version: 1, Data: b}, nil
}

func decode(h ultra.ProviderHandle) (handleData, error) {
	var d handleData
	if len(h.Data) == 0 {
		return d, errors.New("static: empty provider handle")
	}
	return d, json.Unmarshal(h.Data, &d)
}
