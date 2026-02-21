package spec

import (
	"embed"
	"net/http"
)

var (
	//go:embed openapi.yaml
	openapiFS embed.FS
)

// OpenAPIHandler serves the embedded OpenAPI specification.
func OpenAPIHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		content, err := openapiFS.ReadFile("openapi.yaml")
		if err != nil {
			http.Error(w, "openapi spec not available", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/yaml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(content)
	}
}
