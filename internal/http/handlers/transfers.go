package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-ledger/internal/ledger"
	"github.com/go-ledger/internal/money"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

type transferReq struct {
	FromAccountID string `json:"from_account_id"`
	ToAccountID   string `json:"to_account_id"`
	Amount        string `json:"amount"`
	Currency      string `json:"currency"`
}

func Transfer(svc *ledger.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req transferReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "validation_error", "invalid JSON")
			return
		}
		fromID, err := uuid.Parse(req.FromAccountID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "validation_error", "invalid from_account_id")
			return
		}
		toID, err := uuid.Parse(req.ToAccountID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "validation_error", "invalid to_account_id")
			return
		}
		amount, err := money.ParseAmount(req.Amount)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
			return
		}
		if req.Currency == "" {
			writeErr(w, http.StatusBadRequest, "validation_error", "currency is required")
			return
		}
		key := r.Header.Get("Idempotency-Key")
		if key != "" {
			txID, replayed, err := svc.TransferIdempotent(r.Context(), ledger.ScopeTransfer, key, fromID, toID, amount, req.Currency)
			if err != nil {
				mapTransferErr(w, err)
				return
			}
			if replayed {
				w.Header().Set("X-Idempotent-Replay", "true")
			}
			writeJSON(w, http.StatusCreated, map[string]string{"transaction_id": txID.String()})
			return
		}
		txID, err := svc.Transfer(r.Context(), fromID, toID, amount, req.Currency)
		if err != nil {
			mapTransferErr(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"transaction_id": txID.String()})
	}
}

func mapTransferErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ledger.ErrInsufficientFunds):
		writeErr(w, http.StatusConflict, "insufficient_funds", "insufficient funds")
	case errors.Is(err, ledger.ErrNotFound):
		writeErr(w, http.StatusNotFound, "account_not_found", err.Error())
	case errors.Is(err, ledger.ErrIdempotencyInFlight):
		writeErr(w, http.StatusConflict, "idempotency_in_progress", err.Error())
	case errors.Is(err, ledger.ErrIdempotencyMismatch):
		writeErr(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
	case errors.Is(err, ledger.ErrSameAccount), errors.Is(err, ledger.ErrValidation):
		writeErr(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
	default:
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23514" {
			writeErr(w, http.StatusConflict, "insufficient_funds", "insufficient funds")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal_error", "internal error")
	}
}
