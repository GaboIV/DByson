package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/gaboiv/dbyson/internal/config"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gaboiv/dbyson/internal/tunnel"
)

type serviceHint struct {
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Version string `json:"version"`
	Source  string `json:"source"`
}
type networkHint struct {
	Name      string   `json:"name"`
	Addresses []string `json:"addresses"`
}
type discoveryResult struct {
	Containerized   bool          `json:"containerized"`
	DockerAvailable bool          `json:"dockerAvailable"`
	Networks        []networkHint `json:"networks"`
	Services        []serviceHint `json:"services"`
	Note            string        `json:"note"`
}

var discoverySlots = make(chan struct{}, 2)

func (s *Server) discover(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	select {
	case discoverySlots <- struct{}{}:
		defer func() { <-discoverySlots }()
	default:
		writeError(w, 429, "Ya hay una búsqueda en curso")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 7*time.Second)
	defer cancel()
	out := discoveryResult{Networks: []networkHint{}, Services: []serviceHint{}, Note: "Busca servicios MySQL accesibles desde DByson; no necesita credenciales. Las redes listadas pertenecen al servidor, no al navegador."}
	_, err := os.Stat("/.dockerenv")
	out.Containerized = err == nil
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addresses, _ := iface.Addrs()
		n := networkHint{Name: iface.Name, Addresses: []string{}}
		for _, a := range addresses {
			n.Addresses = append(n.Addresses, a.String())
		}
		out.Networks = append(out.Networks, n)
	}
	candidates := []serviceHint{}
	if in.Host != "" {
		if !tunnel.ValidHost(in.Host) || in.Port < 1 || in.Port > 65535 {
			writeError(w, 400, "Host o puerto no válido")
			return
		}
		candidates = append(candidates, serviceHint{Host: in.Host, Port: in.Port, Source: "Destino indicado"})
	} else {
		for _, h := range []string{"host.docker.internal", "mysql", "mariadb", "db", "127.0.0.1"} {
			candidates = append(candidates, serviceHint{Host: h, Port: 3306, Source: "Nombre habitual"})
		}
		for _, p := range []int{3307, 3308} {
			candidates = append(candidates, serviceHint{Host: "host.docker.internal", Port: p, Source: "Puerto publicado"})
		}
		if endpoint := config.Get("DOCKER_DISCOVERY_URL"); endpoint != "" {
			dockerHints, nets, e := dockerInventory(ctx, endpoint)
			if e == nil {
				out.DockerAvailable = true
				candidates = append(candidates, dockerHints...)
				out.Networks = append(out.Networks, nets...)
			} else {
				out.Note += " No se pudo consultar el inventario Docker configurado."
			}
		}
	}
	if !out.DockerAvailable {
		out.Note += " El inventario de contenedores requiere una API Docker configurada; los accesos al host y nombres de servicio funcionan sin ella."
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	workers := make(chan struct{}, 4)
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if len(seen) >= 32 {
			break
		}
		key := fmt.Sprintf("%s:%d", candidate.Host, candidate.Port)
		if seen[key] {
			continue
		}
		seen[key] = true
		wg.Add(1)
		go func(c serviceHint) {
			defer wg.Done()
			select {
			case workers <- struct{}{}:
				defer func() { <-workers }()
			case <-ctx.Done():
				return
			}
			if v := s.mysqlGreeting(ctx, c.Host, c.Port); v != "" {
				c.Version = v
				mu.Lock()
				out.Services = append(out.Services, c)
				mu.Unlock()
			}
		}(candidate)
	}
	wg.Wait()
	writeJSON(w, 200, out)
}

func (s *Server) mysqlGreeting(ctx context.Context, host string, port int) string {
	ctx, cancel := context.WithTimeout(ctx, 1200*time.Millisecond)
	defer cancel()
	conn, err := s.Policy.DirectDialer(time.Second)(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return ""
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	conn.SetDeadline(time.Now().Add(time.Second))
	reader := bufio.NewReaderSize(conn, 256)
	var header [5]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil || header[3] != 0 || header[4] != 10 {
		return ""
	}
	length := int(header[0]) | int(header[1])<<8 | int(header[2])<<16
	if length < 2 {
		return ""
	}
	var version []byte
	for range min(128, length-1) {
		b, e := reader.ReadByte()
		if e != nil {
			return ""
		}
		if b == 0 {
			return strings.Map(func(r rune) rune {
				if r < 32 || r == 127 {
					return -1
				}
				return r
			}, string(version))
		}
		version = append(version, b)
	}
	return ""
}

// The URL is server configured, never accepted from a browser. Use the limited metadata companion.
func dockerInventory(ctx context.Context, endpoint string) ([]serviceHint, []networkHint, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}

	get := func(path string, v any) error {
		req, _ := http.NewRequestWithContext(ctx, "GET", strings.TrimSuffix(endpoint, "/")+path, nil)
		res, e := client.Do(req)
		if e != nil {
			return e
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			return fmt.Errorf("Docker HTTP %d", res.StatusCode)
		}
		return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(v)
	}
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
	if e := get("/containers/json", &containers); e != nil {
		return nil, nil, e
	}
	hints := []serviceHint{}
	for _, c := range containers {
		name := strings.Trim(strings.Join(c.Names, ", "), "/")
		for _, p := range c.Ports {
			if p.PrivatePort != 3306 {
				continue
			}
			if p.PublicPort > 0 {
				hints = append(hints, serviceHint{Host: "host.docker.internal", Port: p.PublicPort, Source: "Docker · " + name})
			}
			for _, n := range c.NetworkSettings.Networks {
				if n.IPAddress != "" {
					hints = append(hints, serviceHint{Host: n.IPAddress, Port: 3306, Source: "Docker · " + name})
				}
			}
		}
	}
	var nets []struct {
		Name string
		IPAM struct{ Config []struct{ Subnet string } }
	}
	networks := []networkHint{}
	if get("/networks", &nets) == nil {
		for _, n := range nets {
			h := networkHint{Name: "Docker · " + n.Name, Addresses: []string{}}
			for _, c := range n.IPAM.Config {
				h.Addresses = append(h.Addresses, c.Subnet)
			}
			networks = append(networks, h)
		}
	}
	return hints, networks, nil
}
