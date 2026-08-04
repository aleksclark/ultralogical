package static_test

import (
	"context"
	"encoding/json"

	uc "github.com/aleksclark/ultracore"
)

func testRes(id uc.ResourceID, spec uc.DevEnvSpec) uc.Resource {
	b, _ := json.Marshal(spec)
	return uc.Resource{ID: id, Kind: uc.ResourceKindDevEnv, Spec: b, State: uc.ResourceRequested}
}

func descriptorsFor(p any, ctx context.Context, id uc.ResourceID) ([]string, error) {
	if l, ok := p.(interface {
		Resources(context.Context, uc.ResourceID) ([]string, error)
	}); ok {
		return l.Resources(ctx, id)
	}
	if l, ok := p.(uc.ResourceLister); ok {
		owned, err := l.ListOwned(ctx)
		if err != nil {
			return nil, err
		}
		for _, o := range owned {
			if o.ResourceID == id {
				return o.Descriptors, nil
			}
		}
		return nil, nil
	}
	return nil, nil
}
