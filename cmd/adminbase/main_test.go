package main

import "testing"

func TestResolveMFAPolicy(t *testing.T) {
	cases := []struct {
		mode, explicit, want string
		err                  bool
	}{
		{"personal", "", "off", false},
		{"team", "", "sensitive", false},
		{"personal", "all", "all", false}, // explicit setting always wins
		{"team", "off", "off", false},
		{"team", "sometimes", "", true},
		{"cluster", "", "", true},
	}
	for _, c := range cases {
		got, err := resolveMFAPolicy(c.mode, c.explicit)
		if (err != nil) != c.err || got != c.want {
			t.Errorf("resolveMFAPolicy(%q, %q) = %q, %v; want %q (err %v)", c.mode, c.explicit, got, err, c.want, c.err)
		}
	}
}

func TestIsLoopback(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:8080": true,
		"[::1]:8080":     true,
		"localhost:8080": true,
		"0.0.0.0:8080":   false,
		":8080":          false,
		"10.0.0.5:8080":  false,
	} {
		if got := isLoopback(addr); got != want {
			t.Errorf("isLoopback(%q) = %v, want %v", addr, got, want)
		}
	}
}
