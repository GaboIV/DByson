package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/adminbase/adminbase/internal/discoveryproxy"
)

func main() {
	socket := os.Getenv("ADMINBASE_DOCKER_SOCKET")
	if socket == "" {
		socket = "/var/run/docker.sock"
	}
	server := &http.Server{Addr: ":8090", Handler: discoveryproxy.Handler(socket), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	log.Fatal(server.ListenAndServe())
}
