// Package observability holds cross-cutting setup shared by both binaries
// that expose metrics (cmd/api, cmd/checker): the Prometheus registry
// construction. Individual metrics live next to the code that produces
// them (internal/delivery/httpapi/middleware, internal/usecase/checker,
// internal/usecase/shortener) — this package is deliberately just plumbing.
package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// NewRegistry returns a Prometheus registry pre-populated with the
// standard Go runtime and process collectors (goroutine counts, GC pauses,
// memory, CPU/RSS, ...), so both binaries expose the same baseline without
// duplicating this setup. Deliberately not prometheus.DefaultRegisterer —
// a package-global registry makes constructing more than one in the same
// process (e.g. across tests) panic on duplicate registration.
func NewRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return reg
}
