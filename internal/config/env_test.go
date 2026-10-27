package config

import (
	"slices"
	"testing"
)

func TestGetReadsCurrentName(t *testing.T) {
	t.Setenv("DBYSON_PROBE_A", "new")
	if got := Get("PROBE_A"); got != "new" {
		t.Fatalf("got %q", got)
	}
}

func TestLegacyNameIsIgnored(t *testing.T) {
	// ADMINBASE_* is not read anymore.
	t.Setenv("ADMINBASE_PROBE_B", "old")
	if got := Get("PROBE_B"); got != "" {
		t.Fatalf("legacy variable leaked into config: %q", got)
	}
	if got := GetOr("PROBE_B", "default"); got != "default" {
		t.Fatalf("got %q", got)
	}
	if !slices.Contains(LegacyPresent(), "ADMINBASE_PROBE_B") {
		t.Fatal("set legacy name must be reported")
	}
}

func TestLegacyPresentIgnoresCurrentNames(t *testing.T) {
	t.Setenv("DBYSON_PROBE_C", "new")
	for _, name := range LegacyPresent() {
		if name == "DBYSON_PROBE_C" {
			t.Fatal("current name reported as legacy")
		}
	}
}
