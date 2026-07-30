// Package mcp implements the minimal Streamable HTTP subset required to
// consume Bezalel. It uses plain JSON-RPC POSTs: initialize, tools/list, and
// tools/call. No MCP types leak into the domain.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

// Tool is one discovered MCP tool.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Result is a text-oriented MCP tool result.
type Result struct {
	Text    string
	IsError bool
}

// Client calls one authenticated MCP endpoint.
type Client struct {
	Endpoint string
	Token    string
	HTTP     *http.Client
	nextID   atomic.Int64
}

// NewClient creates a client with a five-minute request timeout.
func NewClient(endpoint, token string) *Client {
	return &Client{Endpoint: endpoint, Token: token, HTTP: &http.Client{Timeout: 5 * time.Minute}}
}

type request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *Client) rpc(ctx context.Context, method string, params any, out any) error {
	id := c.nextID.Add(1)
	body, err := json.Marshal(request{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("mcp: %s: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return errors.New("mcp: unauthorized")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("mcp: HTTP %d", resp.StatusCode)
	}
	var rpcResp response
	if err := json.Unmarshal(data, &rpcResp); err != nil {
		return fmt.Errorf("mcp: decode response: %w", err)
	}
	if rpcResp.Error != nil {
		return fmt.Errorf("mcp: RPC %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	if out != nil {
		if err := json.Unmarshal(rpcResp.Result, out); err != nil {
			return fmt.Errorf("mcp: decode result: %w", err)
		}
	}
	return nil
}

// Initialize performs the MCP handshake.
func (c *Client) Initialize(ctx context.Context) error {
	return c.rpc(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "ultralogical", "version": "0.1.0"},
	}, &map[string]any{})
}

// Tools lists available tools.
func (c *Client) Tools(ctx context.Context) ([]Tool, error) {
	var result struct {
		Tools []Tool `json:"tools"`
	}
	if err := c.rpc(ctx, "tools/list", nil, &result); err != nil {
		return nil, err
	}
	return result.Tools, nil
}

// Call invokes one tool.
func (c *Client) Call(ctx context.Context, name string, args json.RawMessage) (Result, error) {
	var arguments any = map[string]any{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &arguments); err != nil {
			return Result{}, fmt.Errorf("mcp: invalid tool input: %w", err)
		}
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := c.rpc(ctx, "tools/call", map[string]any{"name": name, "arguments": arguments}, &result); err != nil {
		return Result{}, err
	}
	var text bytes.Buffer
	for _, content := range result.Content {
		if content.Type == "text" {
			text.WriteString(content.Text)
		}
	}
	return Result{Text: text.String(), IsError: result.IsError}, nil
}

// Healthy checks Bezalel's unauthenticated health endpoint.
func Healthy(ctx context.Context, mcpEndpoint string) error {
	healthURL := mcpEndpoint
	if len(healthURL) >= 4 && healthURL[len(healthURL)-4:] == "/mcp" {
		healthURL = healthURL[:len(healthURL)-4] + "/health"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health: HTTP %d", resp.StatusCode)
	}
	return nil
}
