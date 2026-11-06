package registry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// writeCheckTimeout bounds the bd call one refresh makes to learn where bd
// writes. bd answers in well under a second; a hang must not stall health.
const writeCheckTimeout = 5 * time.Second

// BdTarget is the database bd resolves for a workdir. Its fields match
// bdexec.Target, so a caller converts with BdTarget(t).
type BdTarget struct {
	Database string
	Host     string
	Port     int
	Embedded bool
}

// WriteTargets asks bd which database it writes to in workdir; main wires it to
// bdexec.Client.Info. It is a func rather than that type because bdexec's tests
// import this package (through loomtest/livedolt), so this package cannot
// import bdexec.
type WriteTargets func(ctx context.Context, workdir string) (BdTarget, error)

// SetWriteCheck turns on the per-refresh comparison of Loom's read target with
// bd's write target. Call it before the first Refresh. A nil bd turns it off.
func (r *Registry) SetWriteCheck(bd WriteTargets, log *slog.Logger) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.writeTargets = bd
	if log != nil {
		r.log = log
	}
}

// checkWritable compares the database Loom resolved for p with the one bd
// resolves for the same workdir. Reads go over SQL to Loom's target while
// every write goes through bd, so when the two differ a write lands somewhere
// the UI never shows; the project is then read-only with a reason.
//
// Only a project whose config sets database: or server: is checked. Without
// either, Loom derives its target from the same .beads/metadata.json bd reads,
// so there is nothing to disagree about and no subprocess to pay for.
//
// A bd that cannot answer fails open: writes stay enabled and the failure is
// logged once.
func (r *Registry) checkWritable(ctx context.Context, p *project, res resolved) (bool, string) {
	r.mu.RLock()
	bd, log := r.writeTargets, r.log
	r.mu.RUnlock()
	pc := p.cfg
	if bd == nil || (pc.Database == "" && pc.Server == "") || res.Database == "" {
		return true, ""
	}

	ctx, cancel := context.WithTimeout(ctx, writeCheckTimeout)
	defer cancel()
	got, err := bd(ctx, pc.Workdir)
	if err != nil {
		stderr := err.Error()
		// bdexec.BdError carries bd's own words; prefer them to the wrapper.
		var bdErr interface{ Message() string }
		if errors.As(err, &bdErr) {
			stderr = bdErr.Message()
		}
		r.mu.Lock()
		repeated := p.writeCheckWarn == stderr
		p.writeCheckWarn = stderr
		r.mu.Unlock()
		if !repeated {
			log.Warn("cannot ask bd which database it writes to; writes stay enabled",
				"project", pc.Name, "workdir", pc.Workdir, "stderr", stderr)
		}
		return true, ""
	}
	r.mu.Lock()
	p.writeCheckWarn = ""
	r.mu.Unlock()

	loomEmbedded := res.Embedded != nil
	same := got.Database == res.Database && got.Embedded == loomEmbedded
	// The same database name on another server is another database, local or
	// remote. An address either side could not resolve is not compared, so a
	// gap in what bd reports keeps writes enabled rather than blocking them.
	if same && !loomEmbedded && addrKnown(res.Host, res.Port) && addrKnown(got.Host, got.Port) &&
		!sameAddr(res.Host, res.Port, got.Host, got.Port) {
		same = false
	}
	if same {
		return true, ""
	}

	loomTarget := describeTarget(res.Database, res.Host, res.Port, loomEmbedded)
	if !loomEmbedded && !res.Remote && res.Port == 0 {
		loomTarget = describeTarget(res.Database, "", 0, false)
	}
	return false, fmt.Sprintf(
		"Loom reads %s but bd writes to %s, so a write would land where Loom never shows it — make the %s for %q in %s match %s",
		loomTarget,
		describeTarget(got.Database, got.Host, got.Port, got.Embedded),
		overrideNames(pc.Database, pc.Server), pc.Name, r.configName(),
		filepath.Join(pc.Workdir, ".beads", MetadataFileName))
}

// recordWritable stores the outcome of checkWritable on p.
func (r *Registry) recordWritable(p *project, writable bool, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !p.active || r.closed {
		return
	}
	p.Writable = writable
	p.WriteReason = reason
}

func (r *Registry) configName() string {
	if r.cfg.Path != "" {
		return r.cfg.Path
	}
	return "the Loom config"
}

func describeTarget(database, host string, port int, embedded bool) string {
	switch {
	case embedded:
		return fmt.Sprintf("database %q in the embedded store", database)
	case host != "" && port != 0:
		return fmt.Sprintf("database %q on %s", database, net.JoinHostPort(host, strconv.Itoa(port)))
	default:
		return fmt.Sprintf("database %q", database)
	}
}

func overrideNames(database, server string) string {
	switch {
	case database != "" && server != "":
		return "server: and database: overrides"
	case server != "":
		return "server: override"
	default:
		return "database: override"
	}
}

func addrKnown(host string, port int) bool {
	return host != "" && port != 0
}

// sameAddr compares two server addresses, treating every loopback spelling as
// one host.
func sameAddr(hostA string, portA int, hostB string, portB int) bool {
	if portA != portB {
		return false
	}
	if strings.EqualFold(hostA, hostB) {
		return true
	}
	return isLoopback(hostA) && isLoopback(hostB)
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
