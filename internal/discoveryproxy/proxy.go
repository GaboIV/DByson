// Package discoveryproxy exposes only the Docker metadata needed by the connection wizard.
// It never forwards caller-controlled paths, methods, headers, bodies or query strings.
package discoveryproxy

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"time"
)

func Handler(socket string) http.Handler {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", socket)
	}}
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	return metadataHandler(client, "http://docker")
}

func metadataHandler(client *http.Client, base string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || (r.URL.Path != "/containers/json" && r.URL.Path != "/networks") || r.URL.RawQuery != "" {
			http.Error(w, "not found", 404)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, "GET", base+r.URL.Path, nil)
		res, err := client.Do(req)
		if err != nil {
			http.Error(w, "Docker unavailable", 502)
			return
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			http.Error(w, "Docker unavailable", 502)
			return
		}
		dec := json.NewDecoder(io.LimitReader(res.Body, 2<<20))
		var result any
		if r.URL.Path == "/containers/json" {
			var containers []struct {
				Names []string
				Image string
				Ports []struct {
					PrivatePort int
					PublicPort  int
					IP          string
				}
				NetworkSettings struct {
					Networks map[string]struct{ IPAddress string }
				}
			}
			if dec.Decode(&containers) != nil {
				http.Error(w, "invalid metadata", 502)
				return
			}
			result = containers
		} else {
			var networks []struct {
				Name string
				IPAM struct{ Config []struct{ Subnet string } }
			}
			if dec.Decode(&networks) != nil {
				http.Error(w, "invalid metadata", 502)
				return
			}
			result = networks
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(result)
	})
}
