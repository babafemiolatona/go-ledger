package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-ledger/internal/ledger"
	"github.com/google/uuid"
)

type createAccountReq struct {
	OwnerID  string `json:"owner_id"`
	Currency string `json:"currency"`
	Purpose  string `json:"purpose"`
}

func CreateAccount(svc *ledger.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createAccountReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "validation_error", "invalid JSON")
			return
		}
		ownerID, err := uuid.Parse(req.OwnerID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "validation_error", "invalid owner_id")
			return
		}
		acct, err := svc.CreateAccount(r.Context(), ownerID, req.Currency, req.Purpose)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, acct)
	}
}
