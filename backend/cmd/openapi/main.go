package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/go-chi/chi/v5"

	oa "github.com/brygge-klubb/brygge/internal/openapi"
)

func main() {
	router := chi.NewRouter()
	api := oa.NewAPI(router, oa.Config{DocsEnabled: false})

	oa.RegisterAllOperations(api)

	spec, err := json.MarshalIndent(api.OpenAPI(), "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error marshaling spec: %v\n", err)
		os.Exit(1)
	}

	os.Stdout.Write(spec)
	os.Stdout.Write([]byte("\n"))
}
