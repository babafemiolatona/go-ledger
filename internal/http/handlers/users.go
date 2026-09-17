package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-ledger/internal/ledger"
)

func CreateUser(svc *ledger.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Email string `json:"email"`
			Role  string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteErr(w, 400, "validation_error", "invalid JSON")
			return
		}
		if req.Email == "" {
			WriteErr(w, 400, "validation_error", "email required")
			return
		}
		uid, raw, kid, err := svc.CreateUserWithKey(r.Context(), req.Email, req.Role)
		if err != nil {
			WriteErr(w, 400, "validation_error", err.Error())
			return
		}
		writeJSON(w, 201, map[string]string{"user_id": uid.String(), "api_key": raw, "key_id": kid.String()})
	}
}
