// Package config reads DByson's environment variables.
//
// The product was first called AdminBase, so every variable used to be ADMINBASE_*. The DBYSON_*
// name always wins; the old name is still honoured so existing deployments keep working, and
// Deprecated reports it so operators can rename it. Remove the fallback in v1.0.
package config

import (
	"os"
	"sort"
	"strings"
)

const (
	prefix       = "DBYSON_"
	legacyPrefix = "ADMINBASE_"
)

// Get returns DBYSON_<name>, falling back to the legacy ADMINBASE_<name>.
// An explicitly set DBYSON_<name>, even empty, disables the fallback.
func Get(name string) string {
	if v, ok := os.LookupEnv(prefix + name); ok {
		return v
	}
	return os.Getenv(legacyPrefix + name)
}

// GetOr returns Get(name), or fallback when the variable is unset or empty.
func GetOr(name, fallback string) string {
	if v := Get(name); v != "" {
		return v
	}
	return fallback
}

// Deprecated lists the ADMINBASE_* variables currently in effect (set, and not overridden by
// their DBYSON_* counterpart), sorted.
func Deprecated() []string {
	var out []string
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(key, legacyPrefix) {
			continue
		}
		if _, overridden := os.LookupEnv(prefix + strings.TrimPrefix(key, legacyPrefix)); !overridden {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}
