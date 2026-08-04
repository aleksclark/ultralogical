package static

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/provider/handlefmt"
)

// Kind implements uc.ResourceProvider.
func (p *Provider) Kind() uc.ResourceKind { return uc.ResourceKindDevEnv }

// ValidateSpec implements uc.ResourceProvider.
func (p *Provider) ValidateSpec(spec json.RawMessage) error {
	s, err := uc.ParseDevEnvSpec(spec)
	if err != nil {
		return err
	}
	if s.Name == "" {
		return fmt.Errorf("static: spec.name is required")
	}
	return nil
}

// HealthCheck implements uc.ResourceProvider.
func (p *Provider) HealthCheck(ctx context.Context, r uc.Resource) error {
	st, err := p.Status(ctx, r)
	if err != nil {
		return err
	}
	if st.State != uc.ResourceReady {
		if st.Message != "" {
			return fmt.Errorf("static: not ready: %s", st.Message)
		}
		return fmt.Errorf("static: not ready: %s", st.State)
	}
	return nil
}

func (p *Provider) ListOwned(_ context.Context) ([]uc.OwnedResource, error) {
	entries, err := os.ReadDir(p.cfg.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []uc.OwnedResource
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := uc.ResourceID(e.Name())
		out = append(out, uc.OwnedResource{ResourceID: id, Descriptors: []string{"sandbox/" + string(id)}})
	}
	return out, nil
}

func encode(d handleData) (json.RawMessage, error) {
	return handlefmt.EncodeHandle(1, d)
}

func decode(h json.RawMessage) (handleData, error) {
	var d handleData
	if err := handlefmt.DecodeHandle(h, &d); err != nil {
		return d, err
	}
	return d, nil
}

func (p *Provider) Resources(_ context.Context, id uc.ResourceID) ([]string, error) {
	if _, err := os.Stat(p.dir(id)); err != nil {
		return nil, nil
	}
	return []string{"sandbox/" + string(id)}, nil
}
