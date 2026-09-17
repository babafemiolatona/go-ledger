package handlers

import (
	"net/http"
	"os"
)

func Docs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(200)
	_, _ = w.Write([]byte(`<!doctype html><html><head><title>go-ledger docs</title><link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css"></head><body><div id="swagger-ui"></div><script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script><script>SwaggerUIBundle({url:"/docs/openapi.yaml",dom_id:"#swagger-ui"})</script></body></html>`))
}

func OpenAPISpec(w http.ResponseWriter, r *http.Request) {
	b, err := os.ReadFile("docs/openapi.yaml")
	if err != nil {
		b, err = os.ReadFile("/app/docs/openapi.yaml")
		if err != nil {
			http.Error(w, "openapi not found", 404)
			return
		}
	}
	w.Header().Set("Content-Type", "text/yaml")
	w.WriteHeader(200)
	_, _ = w.Write(b)
}
