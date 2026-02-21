package problem

import (
	"encoding/json"
	"net/http"
)

const contentType = "application/problem+json"
const baseTypeURL = "https://errors.paymentapp.com/"

// Details represents RFC 7807 Problem Details.
type Details struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail"`
	Instance  string `json:"instance"`
	RequestID string `json:"request_id"`
}

func Type(slug string) string {
	return baseTypeURL + slug
}

// Write sends RFC 7807-compliant errors.
func Write(w http.ResponseWriter, r *http.Request, status int, problemType, title, detail string) {
	if title == "" {
		title = http.StatusText(status)
	}
	if problemType == "" {
		problemType = "about:blank"
	}
	instance := ""
	requestID := ""
	if r != nil {
		instance = r.URL.Path
		requestID = r.Header.Get("X-Trace-ID")
	}
	if requestID == "" {
		requestID = w.Header().Get("X-Trace-ID")
	}

	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Details{
		Type:      problemType,
		Title:     title,
		Status:    status,
		Detail:    detail,
		Instance:  instance,
		RequestID: requestID,
	})
}
