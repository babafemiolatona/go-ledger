package handlers

import (
	"net/http"

	"github.com/go-ledger/internal/ledger"
	"github.com/google/uuid"
)

func ListAccounts(svc *ledger.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ownerStr := r.URL.Query().Get("owner_id")
		if ownerStr == "" {
			writeErr(w, http.StatusBadRequest, "validation_error", "owner_id is required")
			return
		}
		ownerID, err := uuid.Parse(ownerStr)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "validation_error", "invalid owner_id")
			return
		}
		callerID, _, ok := ledger.CallerFromContext(r.Context())
		if !ok {
			WriteErr(w, http.StatusUnauthorized, "unauthorized", "missing auth")
			return
		}
		if !ledger.IsService(r.Context()) && ownerID != callerID {
			WriteErr(w, http.StatusForbidden, "forbidden", "not owner")
			return
		}
		accounts, err := svc.ListAccounts(r.Context(), ownerID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "internal_error", "internal_error")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"accounts": accounts})
	}
}
