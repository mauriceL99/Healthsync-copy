package agent

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// maxMCPRequestBytes bounds one HTTP request; tool calls are small JSON objects.
const maxMCPRequestBytes = 1 << 20

// HTTPHandler serves MCP over Streamable HTTP at /mcp/<token> and the Garmin
// upload page at /upload/<uploadToken>. Every MCP request
// gets a plain JSON response; there are no server-initiated streams and no
// sessions, which the transport allows. token is called per request, so a
// rotated secret takes effect without a restart.
//
// The token is the only credential: anyone who knows the URL can read the
// health data. It is compared in constant time, and a wrong token gets the
// same 404 as any unknown path.
func (m *MCP) HTTPHandler(token, uploadToken func() (string, error)) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok\n")
	})
	secret := func(token func() (string, error), next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			want, err := token()
			if err != nil {
				m.Env.logf("connector token unavailable: %v", err)
				http.Error(w, "service not ready", http.StatusServiceUnavailable)
				return
			}
			if want == "" || subtle.ConstantTimeCompare([]byte(r.PathValue("token")), []byte(want)) != 1 {
				http.NotFound(w, r)
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("/mcp/{token}", secret(token, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		m.serveHTTP(w, r)
	}))
	mux.HandleFunc("/upload/{token}", secret(uploadToken, m.serveUpload))
	return mux
}

func (m *MCP) serveHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxMCPRequestBytes))
	if err != nil {
		status := http.StatusBadRequest
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, "request body unreadable or too large", status)
		return
	}
	body = bytes.TrimSpace(body)
	batch := len(body) > 0 && body[0] == '['
	var messages []json.RawMessage
	if batch {
		if err := json.Unmarshal(body, &messages); err != nil {
			writeJSON(w, http.StatusBadRequest, parseErrorReply())
			return
		}
	} else {
		messages = []json.RawMessage{body}
	}
	var replies []map[string]any
	for _, raw := range messages {
		var req rpcRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			if !batch {
				writeJSON(w, http.StatusBadRequest, parseErrorReply())
				return
			}
			replies = append(replies, parseErrorReply())
			continue
		}
		if reply := m.respond(r.Context(), req); reply != nil {
			replies = append(replies, reply)
		}
	}
	switch {
	case len(replies) == 0: // only notifications or responses
		w.WriteHeader(http.StatusAccepted)
	case batch:
		writeJSON(w, http.StatusOK, replies)
	default:
		writeJSON(w, http.StatusOK, replies[0])
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
