package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-ledger/internal/ledger"
	"github.com/google/uuid"
)

func GetTransaction(svc *ledger.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		txID, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			writeErr(w, http.StatusBadRequest, "validation_error", "invalid transaction id")
			return
		}
		callerID, _, ok := ledger.CallerFromContext(r.Context())
		if !ok {
			WriteErr(w, http.StatusUnauthorized, "unauthorized", "missing auth")
			return
		}
		d, err := svc.GetTransaction(r.Context(), txID)
		if err != nil {
			if errors.Is(err, ledger.ErrNotFound) {
				writeErr(w, http.StatusNotFound, "transaction_not_found", "transaction not found")
				return
			}
			writeErr(w, http.StatusInternalServerError, "internal_error", "internal error")
			return
		}
		if !ledger.IsService(r.Context()) {
			owns, err := svc.IsTransactionOwner(r.Context(), txID, callerID)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, "internal_error", "internal error")
				return
			}
			if !owns {
				WriteErr(w, http.StatusForbidden, "forbidden", "not owner")
				return
			}
		}
		writeJSON(w, http.StatusOK, d)
	}
}
