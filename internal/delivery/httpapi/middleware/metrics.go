package middleware

import (
	"net/http"
	"strconv"
	"time"
)

// HTTPMetricsRecorder receives one observation per completed request. The
// production implementation (see the sibling httpmetrics package) records
// to Prometheus; this package itself has no Prometheus dependency, so it
// — and every other middleware in this package — stays buildable and
// testable without one, using a fake recorder in tests.
type HTTPMetricsRecorder interface {
	RecordRequest(method, route, status string, duration time.Duration)
}

// WrapMetrics reports one observation per request to rec, labeled with
// route as given explicitly — not the raw URL path. The caller passes
// route at each registration site (e.g. "GET /v1/links/{code}") rather
// than this middleware trying to recover the matched pattern from the
// request, so a code like {code} never becomes its own label value and
// silently explodes cardinality as links accumulate.
//
// A nil rec makes this a no-op passthrough — the same "optional, off
// unless configured" pattern as RateLimit's limiter. Install per-route,
// close to the actual handler, not wrapped around the whole mux —
// precisely because each route needs its own route label.
func WrapMetrics(rec HTTPMetricsRecorder, route string, next http.Handler) http.Handler {
	if rec == nil {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recW := &metricsRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(recW, r)

		rec.RecordRequest(r.Method, route, strconv.Itoa(recW.status), time.Since(start))
	})
}

type metricsRecorder struct {
	http.ResponseWriter
	status int
}

func (r *metricsRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
