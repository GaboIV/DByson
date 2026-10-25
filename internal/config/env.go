// Package config reads DByson's environment variables.
//
// The product was first called AdminBase, so every variable used to be ADMINBASE_*.
// The legacy names were honoured until v1.0; since v1.0 only DBYSON_* is read and
// ADMINBASE_* is ignored. LegacyPresent lists legacy variables still set so the
// server can warn that they have no effect instead of silently misconfiguring.
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

// Get returns DBYSON_<name>, or "" when it is unset or empty.
func Get(name string) string {
	return os.Getenv(prefix + name)
}

// GetOr returns Get(name), or fallback when the variable is unset or empty.
func GetOr(name, fallback string) string {
	if v := Get(name); v != "" {
		return v
	}
	return fallback
}

// LegacyPresent lists the ADMINBASE_* variables currently set, sorted. They
// are ignored: operators should remove them.
func LegacyPresent() []string {
	var out []string
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, legacyPrefix) {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}
