package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-ledger/internal/ledger"
	"github.com/google/uuid"
)

func ListApiKeys(svc *ledger.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, _, ok := ledger.CallerFromContext(r.Context())
		if !ok {
			WriteErr(w, 401, "unauthorized", "missing auth")
			return
		}
		keys, _ := svc.ListApiKeys(r.Context(), uid)
		writeJSON(w, 200, map[string]any{"keys": keys})
	}
}

func CreateApiKey(svc *ledger.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, _, ok := ledger.CallerFromContext(r.Context())
		if !ok {
			WriteErr(w, 401, "unauthorized", "missing auth")
			return
		}
		var req struct {
			Role string `json:"role"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		raw, info, err := svc.CreateApiKey(r.Context(), uid, req.Role)
		if err != nil {
			WriteErr(w, 400, "validation_error", err.Error())
			return
		}
		writeJSON(w, 201, map[string]any{"api_key": raw, "key": info})
	}
}
func RevokeApiKey(svc *ledger.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, _, ok := ledger.CallerFromContext(r.Context())
		if !ok {
			WriteErr(w, 401, "unauthorized", "missing auth")
			return
		}
		kid, _ := uuid.Parse(chi.URLParam(r, "id"))
		if err := svc.RevokeApiKey(r.Context(), uid, kid); err != nil {
			WriteErr(w, 404, "not_found", "key not found")
			return
		}
		w.WriteHeader(204)
	}
}
