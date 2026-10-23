package config

import (
	"slices"
	"testing"
)

func TestNewNameWins(t *testing.T) {
	t.Setenv("DBYSON_PROBE_A", "new")
	t.Setenv("ADMINBASE_PROBE_A", "old")
	if got := Get("PROBE_A"); got != "new" {
		t.Fatalf("got %q", got)
	}
	if slices.Contains(Deprecated(), "ADMINBASE_PROBE_A") {
		t.Fatal("overridden legacy name must not be reported")
	}
}

func TestLegacyFallbackIsReported(t *testing.T) {
	t.Setenv("ADMINBASE_PROBE_B", "old")
	if got := Get("PROBE_B"); got != "old" {
		t.Fatalf("got %q", got)
	}
	if !slices.Contains(Deprecated(), "ADMINBASE_PROBE_B") {
		t.Fatal("legacy name in effect must be reported")
	}
}

func TestExplicitEmptyNewNameDisablesFallback(t *testing.T) {
	// Setting DBYSON_X="" on purpose must not silently pick up an old ADMINBASE_X.
	t.Setenv("DBYSON_PROBE_C", "")
	t.Setenv("ADMINBASE_PROBE_C", "old")
	if got := GetOr("PROBE_C", "default"); got != "default" {
		t.Fatalf("got %q", got)
	}
}
