package handlefmt

import (
	"encoding/json"
	"fmt"
)

// wireHandle is the durable handle envelope. Wire format is identical to the
// historical ProviderHandle marshaling: {"version":1,"data":...}.
type wireHandle struct {
	Version int             `json:"version"`
	Data    json.RawMessage `json:"data"`
}

// EncodeHandle encodes a provider-owned handle payload into the durable wire
// format used by Resource.Handle.
func EncodeHandle(version int, data any) (json.RawMessage, error) {
	if version <= 0 {
		return nil, fmt.Errorf("handlefmt: handle version must be positive")
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("handlefmt: encode handle data: %w", err)
	}
	return json.Marshal(wireHandle{Version: version, Data: raw})
}

// DecodeHandle decodes a durable handle into dst.
func DecodeHandle(h json.RawMessage, dst any) error {
	if len(h) == 0 {
		return fmt.Errorf("handlefmt: empty handle")
	}
	var wire wireHandle
	if err := json.Unmarshal(h, &wire); err != nil {
		return fmt.Errorf("handlefmt: decode handle envelope: %w", err)
	}
	if wire.Version <= 0 {
		return fmt.Errorf("handlefmt: unsupported handle version %d", wire.Version)
	}
	if err := json.Unmarshal(wire.Data, dst); err != nil {
		return fmt.Errorf("handlefmt: decode handle data: %w", err)
	}
	return nil
}
