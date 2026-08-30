package checker

import "time"

// MetricsRecorder receives observations about checks performed. The
// production implementation (see the sibling checkermetrics package)
// records to Prometheus; this package itself has no Prometheus
// dependency, so Service stays independently testable with a fake.
type MetricsRecorder interface {
	ObserveBatch(n int)
	ObserveCheck(up bool, duration time.Duration)
}
