package discoveryproxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetadataOnly(t *testing.T) {
	calls := 0
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" {
			t.Error("forwarded mutation")
		}
		io.WriteString(w, `[{"Names":["/mysql"],"Image":"mysql:8.4","Labels":{"secret":"do-not-forward"},"Mounts":[{"Source":"private"}],"Ports":[{"PrivatePort":3306}],"NetworkSettings":{"Networks":{"dev":{"IPAddress":"172.19.0.2"}}}}]`)
	}))
	defer daemon.Close()
	proxy := metadataHandler(daemon.Client(), daemon.URL)
	for _, route := range []struct{ method, path string }{{"POST", "/containers/json"}, {"GET", "/containers/json?all=1"}, {"GET", "/containers/id/json"}, {"GET", "/version"}, {"DELETE", "/networks"}} {
		rec := httptest.NewRecorder()
		proxy.ServeHTTP(rec, httptest.NewRequest(route.method, route.path, nil))
		if rec.Code != 404 {
			t.Fatal("route exposed", route)
		}
	}
	if calls != 0 {
		t.Fatal("forwarded rejected paths")
	}
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest("GET", "/containers/json", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "mysql:8.4") || strings.Contains(rec.Body.String(), "do-not-forward") || strings.Contains(rec.Body.String(), "private") {
		t.Fatal(rec.Body.String())
	}
}
