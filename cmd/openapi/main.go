package main

import (
	"ai-unisub/internal/unisub"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	out := flag.String("out", "api/openapi.json", "output OpenAPI JSON path")
	flag.Parse()

	document, err := json.Marshal(unisub.ManagementOpenAPI(), json.Deterministic(true), jsontext.WithIndent("  "))
	if err != nil {
		fatal("marshal OpenAPI", err)
	}
	document = append(document, '\n')
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		fatal("create output directory", err)
	}
	if err := os.WriteFile(*out, document, 0o644); err != nil {
		fatal("write OpenAPI", err)
	}
}

func fatal(action string, err error) {
	_, _ = fmt.Fprintf(os.Stderr, "%s: %v\n", action, err)
	os.Exit(1)
}
