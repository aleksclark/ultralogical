package nullresource_test

import (
	"context"
	"sync"
	"testing"

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/provider/conformance"
	"github.com/aleksclark/ultracore/provider/nullresource"
)

func TestNullResourceConformanceCoreOnly(t *testing.T) {
	var mu sync.Mutex
	var current *nullresource.Provider
	factory := func(t *testing.T) uc.ResourceProvider {
		p := nullresource.New()
		mu.Lock()
		current = p
		mu.Unlock()
		return p
	}
	caps, err := nullresource.New().Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	conformance.RunWith(t, factory, conformance.Options{
		SkipToolSurface: true,
		Capabilities:    caps,
		Inspect: func(t *testing.T, ctx context.Context, id uc.ResourceID) []string {
			mu.Lock()
			p := current
			mu.Unlock()
			if p == nil {
				t.Fatal("no provider")
			}
			owned, err := p.ListOwned(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, o := range owned {
				if o.ResourceID == id {
					return o.Descriptors
				}
			}
			return nil
		},
	})
}
