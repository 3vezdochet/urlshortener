package httpmetrics_test

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"urlshortener/internal/delivery/httpapi/middleware/httpmetrics"
)

func TestMetrics_RecordRequest(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := httpmetrics.New(reg)

	m.RecordRequest("POST", "POST /v1/links", "201", 42*time.Millisecond)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() unexpected error: %v", err)
	}

	var foundCounter, foundHistogram bool
	for _, family := range families {
		switch family.GetName() {
		case "http_requests_total":
			for _, metric := range family.GetMetric() {
				labels := map[string]string{}
				for _, lp := range metric.GetLabel() {
					labels[lp.GetName()] = lp.GetValue()
				}
				if labels["method"] == "POST" && labels["route"] == "POST /v1/links" && labels["status"] == "201" {
					foundCounter = true
					if got := metric.GetCounter().GetValue(); got != 1 {
						t.Errorf("counter value = %v, want 1", got)
					}
				}
			}
		case "http_request_duration_seconds":
			for _, metric := range family.GetMetric() {
				if metric.GetHistogram().GetSampleCount() == 1 {
					foundHistogram = true
				}
			}
		}
	}

	if !foundCounter {
		t.Error("http_requests_total series not found")
	}
	if !foundHistogram {
		t.Error("http_request_duration_seconds observation not found")
	}
}
