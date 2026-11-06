package integration

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// MaxBodyBytes caps a JSON request body.
const MaxBodyBytes = 1 << 20

// ErrorBody is the JSON shape of every API error, core and provider alike.
type ErrorBody struct {
	Error string `json:"error"`
	// Code names a failure the UI handles on its own terms rather than by
	// status alone, such as "query_timeout". Empty for most errors.
	Code string `json:"code,omitempty"`
	// Stderr carries a subprocess's own message on a 422 so the UI can show
	// the user exactly what the CLI objected to.
	Stderr string `json:"stderr,omitempty"`
}

// WriteJSON writes body as JSON with status.
func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	// The response is already committed at this point, so a write failure
	// (client hung up) has nowhere to go.
	_ = enc.Encode(body)
}

// WriteError writes an ErrorBody with status.
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, ErrorBody{Error: msg})
}

// DecodeBody decodes a JSON request body into dst, rejecting unknown fields. An
// empty body leaves dst untouched. On failure it writes a 400 and returns
// false.
func DecodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "could not read request body: "+err.Error())
		return false
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return true
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}
