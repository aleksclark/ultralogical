package postgres_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/postgres"
	"github.com/aleksclark/ultracore/testkit/pgtest"
)

// BenchmarkListSessionsByLabel seeds 10k sessions ×8 labels and measures
// equality selector p95. Recorded in the E3 audit; not a CI gate.
func BenchmarkListSessionsByLabel(b *testing.B) {
	ctx := context.Background()
	pool, url := pgtest.NewPool(&testing.T{})
	if err := postgres.Migrate(ctx, url); err != nil {
		b.Fatal(err)
	}
	s := postgres.NewStore(pool)
	defer pool.Close()

	tenant := uc.Tenant{ID: uc.TenantID(uuid.NewString()), Name: "bench"}
	if err := s.Tenants().Create(ctx, tenant); err != nil {
		b.Fatal(err)
	}
	scope := s.Tenant(tenant.ID)
	const n = 10000
	for i := 0; i < n; i++ {
		labels := map[string]string{
			"student": fmt.Sprintf("s%d", i%500),
			"subject": []string{"math", "ela", "science", "history"}[i%4],
			"grade":   fmt.Sprintf("%d", 1+(i%12)),
			"cohort":  fmt.Sprintf("c%d", i%50),
			"flow":    []string{"review", "tutoring", "assessment", "practice"}[i%4],
			"region":  []string{"us", "eu", "apac", "latam"}[i%4],
			"tier":    []string{"free", "pro", "ent", "edu"}[i%4],
			"flag":    fmt.Sprintf("f%d", i%10),
		}
		if err := scope.Sessions().Create(ctx, uc.Session{
			ID: uc.SessionID(uuid.NewString()), Title: fmt.Sprintf("s%d", i), Labels: labels,
		}); err != nil {
			b.Fatal(err)
		}
	}

	sel := []uc.LabelSelector{{Key: "student", Op: "=", Values: []string{"s42"}}}
	// Warmup.
	if _, err := scope.Sessions().List(ctx, sel); err != nil {
		b.Fatal(err)
	}

	latencies := make([]time.Duration, 0, b.N)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		if _, err := scope.Sessions().List(ctx, sel); err != nil {
			b.Fatal(err)
		}
		latencies = append(latencies, time.Since(start))
	}
	b.StopTimer()

	// Simple p95.
	if len(latencies) == 0 {
		return
	}
	// insertion sort for small N in default bench
	for i := 1; i < len(latencies); i++ {
		j := i
		for j > 0 && latencies[j] < latencies[j-1] {
			latencies[j], latencies[j-1] = latencies[j-1], latencies[j]
			j--
		}
	}
	p95 := latencies[(len(latencies)*95)/100]
	b.ReportMetric(float64(p95.Microseconds()), "p95_us")
	b.Logf("p95=%s over %d runs (10k sessions ×8 labels)", p95, len(latencies))
}
