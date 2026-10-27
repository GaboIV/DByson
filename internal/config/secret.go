package config

import (
	"fmt"
	"os"
	"strings"
)

// Secret reads a value from DBYSON_<name> or DBYSON_<name>_FILE. File contents
// are never included in errors. File-based secrets work with Docker secrets.
func Secret(name string) (string, error) {
	value, file := Get(name), Get(name+"_FILE")
	if value != "" && file != "" {
		return "", fmt.Errorf("set only DBYSON_%s or DBYSON_%s_FILE, not both", name, name)
	}
	if file == "" {
		return value, nil
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("read DBYSON_%s_FILE: %w", name, err)
	}
	value = strings.TrimSpace(string(data))
	if value == "" {
		return "", fmt.Errorf("DBYSON_%s_FILE is empty", name)
	}
	return value, nil
}
