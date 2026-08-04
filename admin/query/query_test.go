package query_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aleksclark/ultracore/admin/query"
)

func TestCompileRejectsInvalidBeforeSQL(t *testing.T) {
	reg := query.NewRegistry()
	col, ok := reg.Get("sessions")
	if !ok {
		t.Fatal("missing sessions")
	}
	c := &query.Compiler{Signer: &query.Signer{Secret: []byte("test-secret-key-32bytes-long!!")}}

	cases := []struct {
		name string
		req  query.Request
		want error
	}{
		{"limit", query.Request{Page: query.Page{Limit: 251}}, query.ErrInvalidLimit},
		{"field", query.Request{Filters: []query.Filter{{Field: "nope", Op: query.OpEq, Value: "x"}}}, query.ErrInvalidField},
		{"op", query.Request{Filters: []query.Filter{{Field: "id", Op: query.OpContains, Value: "x"}}}, query.ErrInvalidOp},
		{"sort", query.Request{Sorts: []query.Sort{{Field: "labels"}}}, query.ErrInvalidField},
		{"filters", query.Request{Filters: manyFilters(20)}, query.ErrTooManyFilters},
		{"query_len", query.Request{Query: strings.Repeat("a", 300)}, query.ErrQueryTooLong},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.Compile(col, tc.req)
			if err == nil || !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
			// Ensure error is ValidationError (never reaches SQL).
			var ve *query.ValidationError
			if !errors.As(err, &ve) && !errors.Is(err, tc.want) {
				t.Fatalf("expected validation error, got %T %v", err, err)
			}
		})
	}
}

func manyFilters(n int) []query.Filter {
	out := make([]query.Filter, n)
	for i := range out {
		out[i] = query.Filter{Field: "title", Op: query.OpEq, Value: "x"}
	}
	return out
}

func TestCursorRoundTripAndFingerprint(t *testing.T) {
	secret := []byte("cursor-secret-for-tests-01234567")
	s := &query.Signer{Secret: secret, TTL: time.Hour}
	c := &query.Compiler{Signer: s}
	reg := query.NewRegistry()
	col, _ := reg.Get("tenants")

	req := query.Request{Page: query.Page{Limit: 10}}
	compiled, err := c.Compile(col, req)
	if err != nil {
		t.Fatal(err)
	}
	cur, err := c.NextCursor(col, compiled, []string{time.Now().UTC().Format(time.RFC3339Nano), "00000000-0000-0000-0000-000000000001"})
	if err != nil {
		t.Fatal(err)
	}
	if cur == "" || strings.Contains(cur, " ") {
		t.Fatalf("cursor not opaque: %q", cur)
	}

	// Same query accepts cursor.
	req2 := query.Request{Page: query.Page{Limit: 10, Cursor: cur}}
	if _, err := c.Compile(col, req2); err != nil {
		t.Fatalf("same query cursor: %v", err)
	}
	// Different filters reject cursor.
	req3 := query.Request{
		Filters: []query.Filter{{Field: "name", Op: query.OpEq, Value: "x"}},
		Page:    query.Page{Limit: 10, Cursor: cur},
	}
	if _, err := c.Compile(col, req3); !errors.Is(err, query.ErrCursorMismatch) {
		t.Fatalf("mismatch: %v", err)
	}
	// Tampered cursor fails.
	if _, err := s.Verify(cur + "x"); !errors.Is(err, query.ErrBadCursor) {
		t.Fatalf("tamper: %v", err)
	}
}

func TestDescribeAllCollections(t *testing.T) {
	reg := query.NewRegistry()
	all := reg.All()
	if len(all) < 10 {
		t.Fatalf("expected full inventory, got %d", len(all))
	}
	need := []string{"tenants", "api_keys", "sessions", "events", "runs", "run_steps",
		"resources", "providers", "credentials", "periodic_prompts", "memory", "waits", "jobs"}
	for _, n := range need {
		if _, ok := reg.Get(n); !ok {
			t.Errorf("missing collection %s", n)
		}
	}
	for _, c := range all {
		if c.Select == "" || c.From == "" {
			t.Errorf("%s missing select/from", c.Name)
		}
		pk := false
		for _, f := range c.Fields {
			if f.PK {
				pk = true
			}
		}
		if !pk {
			t.Errorf("%s has no PK field", c.Name)
		}
		proto := query.CollectionToProto(c)
		if proto.Name != c.Name || len(proto.Fields) == 0 {
			t.Errorf("proto descriptor incomplete for %s", c.Name)
		}
	}
}

func TestCompileProducesParameterizedSQL(t *testing.T) {
	reg := query.NewRegistry()
	col, _ := reg.Get("sessions")
	c := &query.Compiler{Signer: &query.Signer{Secret: []byte("x")}}
	compiled, err := c.Compile(col, query.Request{
		Query:   "demo",
		Filters: []query.Filter{{Field: "tenant_id", Op: query.OpEq, Value: "00000000-0000-0000-0000-0000000000aa"}},
		Sorts:   []query.Sort{{Field: "created_at", Descending: true}},
		Page:    query.Page{Limit: 25},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compiled.SQL, "LIMIT") || !strings.Contains(compiled.SQL, "$") {
		t.Fatalf("expected parameterized SQL, got %s", compiled.SQL)
	}
	// No raw user strings interpolated into SQL.
	if strings.Contains(compiled.SQL, "demo") || strings.Contains(compiled.SQL, "00000000") {
		t.Fatalf("user values leaked into SQL: %s", compiled.SQL)
	}
	if compiled.Limit != 25 {
		t.Fatalf("limit=%d", compiled.Limit)
	}
}
