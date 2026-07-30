package localdocker_test

import (
	"testing"

	ultra "github.com/aleksclark/ultralogical"
	"github.com/aleksclark/ultralogical/envprovider/conformance"
	"github.com/aleksclark/ultralogical/envprovider/localdocker"
	"github.com/aleksclark/ultralogical/testkit/harness"
)

func TestConformance(t *testing.T) {
	image := harness.EnsureBezalelImage(t)
	conformance.Run(t, func(t *testing.T) ultra.EnvProvider {
		provider, err := localdocker.New(localdocker.Config{Image: image})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = provider.Close() })
		return provider
	})
}
