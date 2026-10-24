//go:build ignore

package main

import (
	"github.com/gaboiv/dbyson/internal/testpki"
	"log"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: go run scripts/test-pki.go OUTPUT_DIRECTORY")
	}
	if err := testpki.Generate(os.Args[1]); err != nil {
		log.Fatal(err)
	}
}
