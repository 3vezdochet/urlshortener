package checkermetrics_test

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"urlshortener/internal/usecase/checker/checkermetrics"
)

func TestMetrics_ObserveCheck(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := checkermetrics.New(reg)

	m.ObserveCheck(true, 15*time.Millisecond)
	m.ObserveCheck(false, 3*time.Second)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() unexpected error: %v", err)
	}

	counts := map[string]float64{}
	var sawDuration bool
	for _, family := range families {
		switch family.GetName() {
		case "checker_checks_total":
			for _, metric := range family.GetMetric() {
				for _, lp := range metric.GetLabel() {
					if lp.GetName() == "result" {
						counts[lp.GetValue()] = metric.GetCounter().GetValue()
					}
				}
			}
		case "checker_check_duration_seconds":
			for _, metric := range family.GetMetric() {
				if metric.GetHistogram().GetSampleCount() == 2 {
					sawDuration = true
				}
			}
		}
	}

	if counts["up"] != 1 {
		t.Errorf(`checker_checks_total{result="up"} = %v, want 1`, counts["up"])
	}
	if counts["down"] != 1 {
		t.Errorf(`checker_checks_total{result="down"} = %v, want 1`, counts["down"])
	}
	if !sawDuration {
		t.Error("checker_check_duration_seconds did not record 2 observations")
	}
}

func TestMetrics_ObserveBatch(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := checkermetrics.New(reg)

	m.ObserveBatch(7)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() unexpected error: %v", err)
	}

	var found bool
	for _, family := range families {
		if family.GetName() != "checker_claimed_batch_size" {
			continue
		}
		for _, metric := range family.GetMetric() {
			if metric.GetHistogram().GetSampleCount() == 1 {
				found = true
			}
		}
	}
	if !found {
		t.Error("checker_claimed_batch_size did not record an observation")
	}
}
