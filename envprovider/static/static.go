// Package static is the worked example from docs/providers.md: the smallest
// provider that still passes the shared conformance contract unmodified.
//
// One environment is one Bezalel process in an unprivileged mount namespace
// and a private root, with the workspace bind-mounted at the declared
// workdir. That private /work is what lets concurrent environments each own
// the same absolute path without colliding on the host. A worker offers the
// kind when a Bezalel binary is configured; it drives no remote control plane
// of its own.
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

	ultra "github.com/aleksclark/ultralogical"
)

// Config configures the provider. Root holds one directory per environment,
// which is what makes a resource findable again after a restart.
type Config struct {
	Binary string `json:"binary,omitempty"` // the Bezalel executable environments run
	Root   string `json:"root,omitempty"`   // where per-environment state lives
}

// Provider implements ultra.EnvProvider on local processes.
type Provider struct{ cfg Config }

// handleData is the persisted identity of one environment: enough to find the
// process again from another worker, which is what makes status and terminate
// meaningful after a crash.
type handleData struct {
	EnvID string `json:"env_id"`
	PID   int    `json:"pid"`
	Port  int    `json:"port"`
}

// New builds a provider. A missing Bezalel binary is refused here rather than
// discovered at provision time, so a misconfiguration surfaces at registration.
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

func (p *Provider) dir(envID ultra.EnvID) string { return filepath.Join(p.cfg.Root, string(envID)) }

// Provision starts one sandboxed Bezalel. The workspace directory is created
// before the sandbox so a restart can reuse it, which is why this provider can
// honestly claim to preserve workspaces.
func (p *Provider) Provision(_ context.Context, envID ultra.EnvID, spec ultra.EnvSpec, token string) (ultra.ProviderHandle, error) {
	workdir := spec.Workdir
	if workdir == "" {
		workdir = "/work"
	}
	workspace := filepath.Join(p.dir(envID), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return ultra.ProviderHandle{}, fmt.Errorf("static: create workspace: %w", err)
	}
	port, err := freePort()
	if err != nil {
		return ultra.ProviderHandle{}, err
	}
	logPath := filepath.Join(p.dir(envID), "sandbox.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return ultra.ProviderHandle{}, fmt.Errorf("static: create sandbox log: %w", err)
	}
	cmd := exec.Command("unshare", "--map-root-user", "--mount", "--pid", "--fork",
		"/bin/sh", "-c", sandboxScript(p.cfg.Binary, p.dir(envID), workspace, workdir, port))
	cmd.Env = append(os.Environ(), "BEZALEL_AUTH_TOKEN="+token)
	for key, value := range spec.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	// Stderr lands in the environment directory so a failed sandbox explains
	// itself instead of vanishing into the worker's own logs.
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// The sandbox is its own process group, so terminate can reap the whole
	// tree rather than orphaning the namespace's children.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return ultra.ProviderHandle{}, fmt.Errorf("static: start sandbox: %w", err)
	}
	// The process is released rather than waited on: this provider hands the
	// environment's lifetime to the platform, which terminates it explicitly.
	go func() {
		_ = cmd.Wait()
		_ = logFile.Close()
	}()
	return encode(handleData{EnvID: string(envID), PID: cmd.Process.Pid, Port: port})
}

// sandboxScript builds a private root on a tmpfs and chroots into it. The
// workspace is bind-mounted at the declared workdir so every tool call that
// names that absolute path lands in this environment alone.
//
// Device nodes are bind-mounted individually rather than via --rbind of /dev:
// rbind of the host's /dev hangs on some hardened runners (the failure that
// forced an earlier withdrawal), and userns root cannot mknod either.
func sandboxScript(binary, dir, workspace, workdir string, port int) string {
	root := filepath.Join(dir, "root")
	return fmt.Sprintf(`set -e
mkdir -p %[1]s
mount -t tmpfs -o size=64m tmpfs %[1]s
mkdir -p %[1]s/usr %[1]s/proc %[1]s/dev %[1]s/tmp %[1]s/etc %[1]s/opt %[1]s%[3]s
mount --bind /usr %[1]s/usr
mount -t proc proc %[1]s/proc
touch %[1]s/dev/null %[1]s/dev/zero %[1]s/dev/urandom %[1]s/dev/tty
mount --bind /dev/null %[1]s/dev/null
mount --bind /dev/zero %[1]s/dev/zero
mount --bind /dev/urandom %[1]s/dev/urandom
mount --bind /dev/tty %[1]s/dev/tty 2>/dev/null || true
mount --bind %[2]s %[1]s%[3]s
cp %[4]s %[1]s/opt/bezalel
# /bin and /lib must resolve into the bound /usr: Bezalel shells out to bash
# and coreutils by those absolute paths, and a missing /bin/bash is the
# "no output / exit 1" every tool call would otherwise produce.
ln -s usr/bin %[1]s/bin
ln -s usr/lib %[1]s/lib
ln -s usr/lib64 %[1]s/lib64 2>/dev/null || true
ln -s usr/sbin %[1]s/sbin 2>/dev/null || true
exec env -i PATH=/usr/bin:/bin HOME=%[3]s BEZALEL_AUTH_TOKEN="$BEZALEL_AUTH_TOKEN" \
	chroot %[1]s /opt/bezalel --workdir %[3]s --port %[5]d --host 127.0.0.1
`, root, workspace, workdir, binary, port)
}

// Status reports the sandbox's real condition. A process that is gone is
// failed, not merely unready: something outside the platform killed it, and
// reconciliation has to see that.
func (p *Provider) Status(_ context.Context, handle ultra.ProviderHandle) (ultra.ProviderStatus, error) {
	d, err := decode(handle)
	if err != nil {
		return ultra.ProviderStatus{}, err
	}
	// Signal zero asks the kernel whether the process still exists.
	if err := syscall.Kill(d.PID, 0); err != nil {
		message := "sandbox process is gone"
		if body, readErr := os.ReadFile(filepath.Join(p.dir(ultra.EnvID(d.EnvID)), "sandbox.log")); readErr == nil && len(body) > 0 {
			// Cap the log so a noisy failure cannot flood the status channel.
			trimmed := strings.TrimSpace(string(body))
			if len(trimmed) > 512 {
				trimmed = trimmed[len(trimmed)-512:]
			}
			message = "sandbox process is gone: " + trimmed
		}
		return ultra.ProviderStatus{State: ultra.EnvFailed, Message: message}, nil
	}
	return ultra.ProviderStatus{State: ultra.EnvReady}, nil
}

// Endpoint returns the tool endpoint the sandbox publishes.
func (p *Provider) Endpoint(_ context.Context, handle ultra.ProviderHandle) (string, error) {
	d, err := decode(handle)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("http://127.0.0.1:%d/mcp", d.Port), nil
}

// Restart replaces the process with one carrying the rotated token. Only the
// process is stopped, never the environment's directory: the workspace lives
// there, and removing it would break the preservation this provider claims.
func (p *Provider) Restart(ctx context.Context, envID ultra.EnvID, handle ultra.ProviderHandle, spec ultra.EnvSpec, token string) (ultra.ProviderHandle, error) {
	stop(handle)
	return p.Provision(ctx, envID, spec, token)
}

// Terminate stops the sandbox and removes its state. It is idempotent:
// releasing what is already gone is success, or a retry would turn a completed
// cleanup into a failure.
func (p *Provider) Terminate(_ context.Context, handle ultra.ProviderHandle) error {
	d, err := decode(handle)
	if err != nil {
		// A handle that was never written means nothing was created.
		return nil
	}
	stop(handle)
	if err := os.RemoveAll(p.dir(ultra.EnvID(d.EnvID))); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("static: remove environment state: %w", err)
	}
	return nil
}

// stop kills the sandbox's process group. The negative pid is what reaps the
// namespace's children along with the shell that created them; a lone process
// kill would leave the agent running inside an orphaned namespace.
func stop(handle ultra.ProviderHandle) {
	d, err := decode(handle)
	if err != nil {
		return
	}
	_ = syscall.Kill(-d.PID, syscall.SIGKILL)
	_ = syscall.Kill(d.PID, syscall.SIGKILL)
}

// Resources implements ultra.EnvResourceLister, which is what turns
// "terminated" into a checkable claim rather than an absence of evidence.
func (p *Provider) Resources(_ context.Context, envID ultra.EnvID) ([]string, error) {
	if _, err := os.Stat(p.dir(envID)); err != nil {
		return nil, nil
	}
	return []string{"sandbox/" + string(envID)}, nil
}

// Probe implements ultra.CapabilityProber. The sandbox needs unprivileged user
// namespaces, so the probe checks for them rather than assuming: a host without
// them must report the fact at registration instead of failing every provision.
func (p *Provider) Probe(context.Context) (ultra.ProviderCapabilities, error) {
	capabilities := ultra.ProviderCapabilities{Kind: ultra.ProviderKindStatic, Notes: map[ultra.ProviderCapability]string{
		ultra.CapabilityNamespaceIsolation: "environments share the host kernel and network namespace",
		ultra.CapabilityResourceQuota:      "the example provider enforces no ceiling",
	}}
	if err := exec.Command("unshare", "--map-root-user", "--mount", "true").Run(); err != nil {
		return capabilities, fmt.Errorf("static: this host has no unprivileged user namespaces: %w", err)
	}
	capabilities.Supported = append(capabilities.Supported,
		ultra.CapabilityEnumeratesResources,
		ultra.CapabilityRestartPreservesWorkspace,
		ultra.CapabilityServesToolEndpoint,
	)
	return capabilities, nil
}

// freePort reserves a port the sandbox will bind. A reserved-then-released port
// can race, which is why Provision hands it straight to the process.
func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("static: reserve port: %w", err)
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func encode(d handleData) (ultra.ProviderHandle, error) {
	body, err := json.Marshal(d)
	if err != nil {
		return ultra.ProviderHandle{}, fmt.Errorf("static: encode handle: %w", err)
	}
	return ultra.ProviderHandle{Version: 1, Data: body}, nil
}

func decode(h ultra.ProviderHandle) (handleData, error) {
	var d handleData
	if len(h.Data) == 0 {
		return d, errors.New("static: empty provider handle")
	}
	if err := json.Unmarshal(h.Data, &d); err != nil {
		return d, fmt.Errorf("static: decode handle: %w", err)
	}
	return d, nil
}
