package bdexec

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// KVList returns the flat key/value object emitted by bd kv list --json.
// Missing keys are discovered by absence here; bd kv get cannot distinguish
// them by exit status.
func (c *Client) KVList(ctx context.Context, workdir string) (map[string]string, error) {
	args := []string{"kv", "list", "--json"}
	stdout, stderr, err := c.runner.Run(ctx, workdir, args...)
	if err != nil {
		return nil, newBdError(args, stdout, stderr, err)
	}
	if strings.TrimSpace(stdout) == "" {
		return map[string]string{}, nil
	}
	var rawValues map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &rawValues); err != nil {
		return nil, fmt.Errorf("kv list: decode JSON: %w", err)
	}
	values := make(map[string]string, len(rawValues))
	for key, raw := range rawValues {
		var value string
		if err := json.Unmarshal(raw, &value); err == nil {
			values[key] = value
			continue
		}
		values[key] = string(raw)
	}
	return values, nil
}

// KVGet reads one key. found comes from the JSON envelope's `found` field, not
// the exit status: bd exits 0 for an absent key, so an exit-code test would
// report every miss as a hit with an empty value.
func (c *Client) KVGet(ctx context.Context, workdir, key string) (value string, found bool, err error) {
	if !ValidToken(key) {
		return "", false, fmt.Errorf("kv get: %w: key %q", ErrInvalidID, key)
	}
	args := []string{"kv", "get", key, "--json"}
	stdout, stderr, err := c.runner.Run(ctx, workdir, args...)
	if err != nil {
		return "", false, newBdError(args, stdout, stderr, err)
	}
	var envelope struct {
		Found bool   `json:"found"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		return "", false, fmt.Errorf("kv get %s: decode JSON: %w", key, err)
	}
	if !envelope.Found {
		return "", false, nil
	}
	return envelope.Value, true, nil
}

// KVSet stores one value under a validated key.
func (c *Client) KVSet(ctx context.Context, workdir, key, value string) error {
	if !ValidToken(key) {
		return fmt.Errorf("kv set: %w: key %q", ErrInvalidID, key)
	}
	if err := checkPositionalText("kv set", "value", value); err != nil {
		return err
	}
	return c.exec(ctx, workdir, []string{"kv", "set", key, value})
}

// KVClear removes one validated key.
func (c *Client) KVClear(ctx context.Context, workdir, key string) error {
	if !ValidToken(key) {
		return fmt.Errorf("kv clear: %w: key %q", ErrInvalidID, key)
	}
	return c.exec(ctx, workdir, []string{"kv", "clear", key})
}
