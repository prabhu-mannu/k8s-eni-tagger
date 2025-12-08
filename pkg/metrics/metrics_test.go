package metrics

import (
	"testing"
)

func TestMetricsInit(t *testing.T) {
	// Just accessing the variable triggers init if not already done.
	// We can check if they are not nil.
	if AWSAPILatency == nil {
		t.Error("AWSAPILatency is nil")
	}
	if CacheHitsTotal == nil {
		t.Error("CacheHitsTotal is nil")
	}
	if CacheMissesTotal == nil {
		t.Error("CacheMissesTotal is nil")
	}
}
