package registry

import (
	"sync/atomic"
	_ "unsafe" // for go:linkname

	_ "github.com/dolthub/driver/v2"
)

// driverMetricsDisabled is the driver's usage-metrics switch. On the first
// engine open in a process the driver otherwise creates
// .dolt/temptf/dolt_embedded_metrics in the store it opens, which is a write
// to a workspace Loom only reads. The driver reads its switch only from
// DOLT_METRICS_DISABLED in its own init, before any Loom code runs, so Loom
// sets the variable itself. If the driver renames it, the linked variable is
// nil and Loom refuses to start rather than write to workspaces.
//
//go:linkname driverMetricsDisabled github.com/dolthub/driver/v2.metricsDisabled
var driverMetricsDisabled *atomic.Bool

func init() {
	if driverMetricsDisabled == nil {
		panic("registry: github.com/dolthub/driver/v2.metricsDisabled is gone; the embedded driver would write a metrics file into every workspace it opens")
	}
	driverMetricsDisabled.Store(true)
}
