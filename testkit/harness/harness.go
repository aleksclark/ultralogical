// Package harness boots the real stack for functional tests: real Postgres
// (shared container, fresh database per test), migrations, and ultrad as a
// real child process on a random port. Tests interact only through the
// public API via testclient — the same artifacts users consume.
package harness

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	ultra "github.com/aleksclark/ultralogical"
	"github.com/aleksclark/ultralogical/postgres"
	"github.com/aleksclark/ultralogical/testkit/pgtest"
	"github.com/aleksclark/ultralogical/testkit/testclient"
)

// Seeded identities. The harness provisions two orgs with one user each so
// tenant-isolation is testable out of the box.
const (
	TokenAlice = "tok-alice"
	TokenBob   = "tok-bob"
	EmailAlice = "alice@example.com"
	EmailBob   = "bob@example.com"
)

// Stack is a running ultrad + database with seeded identities.
type Stack struct {
	BaseURL     string
	DatabaseURL string
	OrgA        ultra.Org
	OrgB        ultra.Org
	Alice       ultra.User
	Bob         ultra.User
	Store       *postgres.Store
}

var (
	buildOnce sync.Once
	buildErr  error
	ultradBin string
)

// binary builds ultrad once per test process.
func binary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "ultrad-bin-*")
		if err != nil {
			buildErr = err
			return
		}
		ultradBin = filepath.Join(dir, "ultrad")
		cmd := exec.Command("go", "build", "-o", ultradBin, "github.com/aleksclark/ultralogical/cmd/ultrad")
		cmd.Env = os.Environ()
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("build ultrad: %w\n%s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return ultradBin
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// Up boots a full stack and registers cleanup on the test.
func Up(t *testing.T) *Stack {
	t.Helper()
	ctx := context.Background()

	bin := binary(t)
	dbURL := pgtest.NewDB(t)
	if err := postgres.Migrate(ctx, dbURL); err != nil {
		t.Fatal(err)
	}

	store, pool, err := postgres.Connect(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	stack := &Stack{DatabaseURL: dbURL, Store: store}
	stack.seed(t, store)

	port := freePort(t)
	stack.BaseURL = fmt.Sprintf("http://127.0.0.1:%d", port)

	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		"DATABASE_URL="+dbURL,
		fmt.Sprintf("ULTRA_ADDR=127.0.0.1:%d", port),
		fmt.Sprintf("ULTRA_DEV_TOKENS=%s=%s,%s=%s", TokenAlice, EmailAlice, TokenBob, EmailBob),
		"ULTRA_MIGRATE=false",
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	waitHealthy(t, stack.BaseURL)
	return stack
}

func (s *Stack) seed(t *testing.T, store *postgres.Store) {
	t.Helper()
	ctx := context.Background()

	s.OrgA = ultra.Org{ID: ultra.OrgID(uuid.NewString()), Name: "org-a"}
	s.OrgB = ultra.Org{ID: ultra.OrgID(uuid.NewString()), Name: "org-b"}
	s.Alice = ultra.User{ID: ultra.UserID(uuid.NewString()), Email: EmailAlice, Display: "Alice"}
	s.Bob = ultra.User{ID: ultra.UserID(uuid.NewString()), Email: EmailBob, Display: "Bob"}

	for _, org := range []ultra.Org{s.OrgA, s.OrgB} {
		if err := store.Orgs().Create(ctx, org); err != nil {
			t.Fatal(err)
		}
	}
	for _, user := range []ultra.User{s.Alice, s.Bob} {
		if err := store.Users().Create(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	memberships := []ultra.OrgMember{
		{OrgID: s.OrgA.ID, UserID: s.Alice.ID, Role: ultra.OrgRoleOwner},
		{OrgID: s.OrgB.ID, UserID: s.Bob.ID, Role: ultra.OrgRoleOwner},
	}
	for _, m := range memberships {
		if err := store.Orgs().AddMember(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
}

func waitHealthy(t *testing.T, baseURL string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("ultrad at %s never became healthy", baseURL)
}

// AliceClient returns a client authenticated as Alice (owner of OrgA).
func (s *Stack) AliceClient() *testclient.Client { return testclient.New(s.BaseURL, TokenAlice) }

// BobClient returns a client authenticated as Bob (owner of OrgB).
func (s *Stack) BobClient() *testclient.Client { return testclient.New(s.BaseURL, TokenBob) }
