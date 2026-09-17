package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-ledger/internal/ledger"
	"github.com/go-ledger/internal/money"
	"github.com/google/uuid"
)

type holdReq struct {
	FromAccountID string `json:"from_account_id"`
	ToAccountID   string `json:"to_account_id"`
	Amount        string `json:"amount"`
	Currency      string `json:"currency"`
	ExpiresAt     string `json:"expires_at"`
}

func CreateHold(svc *ledger.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		callerID, _, ok := ledger.CallerFromContext(r.Context())
		if !ok {
			WriteErr(w, 401, "unauthorized", "missing auth")
			return
		}
		var req holdReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteErr(w, 400, "validation_error", "invalid JSON")
			return
		}
		fromID, _ := uuid.Parse(req.FromAccountID)
		toID, _ := uuid.Parse(req.ToAccountID)
		amt, err := money.ParseAmount(req.Amount)
		if err != nil {
			WriteErr(w, 422, "validation_error", err.Error())
			return
		}
		exp, err := time.Parse(time.RFC3339, req.ExpiresAt)
		if err != nil {
			WriteErr(w, 422, "validation_error", "expires_at must be RFC3339")
			return
		}
		if !ledger.IsService(r.Context()) {
			owner, err := svc.GetAccountOwner(r.Context(), fromID)
			if err != nil {
				WriteErr(w, 404, "account_not_found", "account not found")
				return
			}
			if owner != callerID {
				WriteErr(w, 403, "forbidden", "not owner of source")
				return
			}
		}
		id, err := svc.Hold(r.Context(), fromID, toID, amt, req.Currency, exp)
		if err != nil {
			if errors.Is(err, ledger.ErrInsufficientFunds) {
				WriteErr(w, 409, "insufficient_funds", "insufficient funds")
				return
			}
			if errors.Is(err, ledger.ErrNotFound) {
				WriteErr(w, 404, "account_not_found", err.Error())
				return
			}
			WriteErr(w, 422, "validation_error", err.Error())
			return
		}
		writeJSON(w, 201, map[string]string{"transaction_id": id.String()})
	}
}
func CaptureHold(svc *ledger.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		callerID, _, ok := ledger.CallerFromContext(r.Context())
		if !ok {
			WriteErr(w, 401, "unauthorized", "missing auth")
			return
		}
		holdID, _ := uuid.Parse(chi.URLParam(r, "id"))
		if !ledger.IsService(r.Context()) {
			_, owner, err := svc.GetHoldSourceOwner(r.Context(), holdID)
			if err != nil {
				WriteErr(w, 404, "not_found", "hold not found")
				return
			}
			if owner != callerID {
				WriteErr(w, 403, "forbidden", "not owner of hold")
				return
			}
		}
		if err := svc.Capture(r.Context(), holdID); err != nil {
			if errors.Is(err, ledger.ErrNotFound) {
				WriteErr(w, 404, "not_found", err.Error())
				return
			}
			WriteErr(w, 422, "validation_error", err.Error())
			return
		}
		w.WriteHeader(204)
	}
}
func ReleaseHold(svc *ledger.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		callerID, _, ok := ledger.CallerFromContext(r.Context())
		if !ok {
			WriteErr(w, 401, "unauthorized", "missing auth")
			return
		}
		holdID, _ := uuid.Parse(chi.URLParam(r, "id"))
		if !ledger.IsService(r.Context()) {
			_, owner, err := svc.GetHoldSourceOwner(r.Context(), holdID)
			if err != nil {
				WriteErr(w, 404, "not_found", "hold not found")
				return
			}
			if owner != callerID {
				WriteErr(w, 403, "forbidden", "not owner of hold")
				return
			}
		}
		if err := svc.Release(r.Context(), holdID); err != nil {
			if errors.Is(err, ledger.ErrNotFound) {
				WriteErr(w, 404, "not_found", err.Error())
				return
			}
			WriteErr(w, 422, "validation_error", err.Error())
			return
		}
		w.WriteHeader(204)
	}
}
