package shortenermetrics_test

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"urlshortener/internal/usecase/shortener"
	"urlshortener/internal/usecase/shortener/shortenermetrics"
)

func TestMetrics_ObserveCacheLookup(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := shortenermetrics.New(reg)

	m.ObserveCacheLookup(shortener.CacheOutcomeHit)
	m.ObserveCacheLookup(shortener.CacheOutcomeHit)
	m.ObserveCacheLookup(shortener.CacheOutcomeMiss)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() unexpected error: %v", err)
	}

	counts := map[string]float64{}
	for _, family := range families {
		if family.GetName() != "shortener_cache_lookups_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, lp := range metric.GetLabel() {
				if lp.GetName() == "outcome" {
					counts[lp.GetValue()] = metric.GetCounter().GetValue()
				}
			}
		}
	}

	if counts[shortener.CacheOutcomeHit] != 2 {
		t.Errorf("hit count = %v, want 2", counts[shortener.CacheOutcomeHit])
	}
	if counts[shortener.CacheOutcomeMiss] != 1 {
		t.Errorf("miss count = %v, want 1", counts[shortener.CacheOutcomeMiss])
	}
}
