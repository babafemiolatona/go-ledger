package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-ledger/internal/ledger"
	"github.com/google/uuid"
)

func GetStatement(svc *ledger.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		acctID, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			writeErr(w, http.StatusBadRequest, "validation_error", "invalid account id")
			return
		}
		q := r.URL.Query()
		var cursor int64
		if s := q.Get("cursor"); s != "" {
			cursor, err = strconv.ParseInt(s, 10, 64)
			if err != nil || cursor < 0 {
				writeErr(w, http.StatusBadRequest, "validation_error", "invalid cursor")
				return
			}
		}
		limit := 20
		if s := q.Get("limit"); s != "" {
			l, err := strconv.Atoi(s)
			if err != nil || l <= 0 || l > 100 {
				writeErr(w, http.StatusBadRequest, "validation_error", "limit must be 1..100")
				return
			}
			limit = l
		}
		page, err := svc.GetStatement(r.Context(), acctID, cursor, limit)
		if err != nil {
			if errors.Is(err, ledger.ErrNotFound) {
				writeErr(w, http.StatusNotFound, "account_not_found", "account not found")
				return
			}
			writeErr(w, http.StatusInternalServerError, "internal_error", "internal error")
			return
		}
		writeJSON(w, http.StatusOK, page)
	}
}
