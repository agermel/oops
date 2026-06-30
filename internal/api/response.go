package api

import (
	"encoding/json"
	"net/http"
)

// writeJSON writes a JSON response with status 200.
func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(payload)
}

// writeJSONError writes a JSON error response: {"error":"msg"}.
func writeJSONError(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// writeJSONOK writes {"status":"ok"}.
func writeJSONOK(w http.ResponseWriter) {
	writeJSON(w, map[string]string{"status": "ok"})
}
