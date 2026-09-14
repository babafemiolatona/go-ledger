package handlers

import (
	"encoding/json"
	"net/http"
)

type errBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type envelope struct {
	Error *errBody `json:"error,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, envelope{
		Error: &errBody{
			Code:    code,
			Message: message,
		},
	})
}
