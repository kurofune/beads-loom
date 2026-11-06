package bdexec

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Target is the database bd resolves for a workdir, which is where every
// write Loom sends through bd lands.
type Target struct {
	Database string
	// Host and Port are empty for an embedded store.
	Host string
	Port int
	// Embedded is true when bd writes its in-process embedded store.
	Embedded bool
}

// Info asks bd which database it writes to in workdir.
//
// It runs `bd dolt show --json`, not `bd info --json`: bd 1.3.2's info prints
// only database_path (the .beads/dolt directory) and mode "direct" for a
// server-mode workspace, never the database name, while dolt show prints the
// database, host, port and embedded flag bd actually resolved.
func (c *Client) Info(ctx context.Context, workdir string) (Target, error) {
	args := []string{"dolt", "show", "--json"}
	stdout, stderr, err := c.runner.Run(ctx, workdir, args...)
	if err != nil {
		return Target{}, newBdError(args, stdout, stderr, err)
	}
	var shown struct {
		Database string `json:"database"`
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Embedded bool   `json:"embedded"`
	}
	if err := json.Unmarshal([]byte(stdout), &shown); err != nil {
		return Target{}, fmt.Errorf("dolt show: decode JSON: %w (stderr: %s)", err, strings.TrimSpace(stderr))
	}
	if strings.TrimSpace(shown.Database) == "" {
		return Target{}, fmt.Errorf("dolt show: output names no database (stderr: %s)", strings.TrimSpace(stderr))
	}
	t := Target{Database: shown.Database, Embedded: shown.Embedded}
	if !shown.Embedded {
		t.Host, t.Port = shown.Host, shown.Port
	}
	return t, nil
}
