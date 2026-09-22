package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-ledger/internal/ledger"
	"github.com/google/uuid"
)

func CreateWebhook(svc *ledger.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, _, ok := ledger.CallerFromContext(r.Context())
		if !ok {
			WriteErr(w, 401, "unauthorized", "missing auth")
			return
		}
		var req struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteErr(w, 400, "validation_error", "invalid JSON")
			return
		}
		ep, secret, err := svc.CreateWebhookEndpoint(r.Context(), uid, req.URL)
		if err != nil {
			WriteErr(w, 400, "validation_error", err.Error())
			return
		}
		writeJSON(w, 201, map[string]any{
			"id": ep.ID.String(), "url": ep.URL, "status": ep.Status,
			"secret": secret, "created_at": ep.CreatedAt,
		})
	}
}

func ListWebhooks(svc *ledger.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, _, ok := ledger.CallerFromContext(r.Context())
		if !ok {
			WriteErr(w, 401, "unauthorized", "missing auth")
			return
		}
		eps, err := svc.ListWebhookEndpoints(r.Context(), uid, ledger.IsService(r.Context()))
		if err != nil {
			WriteErr(w, 500, "internal_error", "internal error")
			return
		}
		writeJSON(w, 200, map[string]any{"webhooks": eps})
	}
}

func ListWebhookDeliveries(svc *ledger.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, _, ok := ledger.CallerFromContext(r.Context())
		if !ok {
			WriteErr(w, 401, "unauthorized", "missing auth")
			return
		}
		var endpointID *uuid.UUID
		if s := r.URL.Query().Get("webhook_id"); s != "" {
			id, err := uuid.Parse(s)
			if err != nil {
				WriteErr(w, 400, "validation_error", "invalid webhook_id")
				return
			}
			if !ledger.IsService(r.Context()) {
				owner, err := svc.GetWebhookEndpointOwner(r.Context(), id)
				if err != nil || owner != uid {
					WriteErr(w, 404, "not_found", "webhook not found")
					return
				}
			}
			endpointID = &id
		}
		deliveries, err := svc.ListWebhookDeliveries(r.Context(), uid, ledger.IsService(r.Context()), endpointID, r.URL.Query().Get("status"))
		if err != nil {
			WriteErr(w, 400, "validation_error", err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"deliveries": deliveries})
	}
}
