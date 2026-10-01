// Command frontend serves a small web page that asks the agent to summarise a
// Twitch channel's chat. It calls a local agent over the ADK REST API, or an
// Agent Engine deployment when AGENT_ENGINE_ID is set.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

//go:embed static
var static embed.FS

const appName = "twitch_chat_agent"

type summariseRequest struct {
	Channel   string `json:"channel"`
	Seconds   int    `json:"seconds"`
	Messages  int    `json:"messages"`
	SessionID string `json:"sessionId"`
	// Stored reads saved messages instead of live chat. Channel is then the
	// message_data file name.
	Stored bool `json:"stored"`
}

type summariseResponse struct {
	Reply     string `json:"reply,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	Error     string `json:"error,omitempty"`
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx := context.Background()
	client, err := newClient(ctx)
	if err != nil {
		return err
	}

	staticFS, err := fs.Sub(static, "static")
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(staticFS))
	mux.HandleFunc("POST /api/summarise", summariseHandler(client))

	addr := firstNonEmpty(os.Getenv("FRONTEND_ADDR"), ":8090")
	log.Printf("frontend listening on http://localhost%s", addr)
	return http.ListenAndServe(addr, mux)
}

func newClient(ctx context.Context) (AgentClient, error) {
	if id := os.Getenv("AGENT_ENGINE_ID"); id != "" {
		project := os.Getenv("GOOGLE_CLOUD_PROJECT")
		location := firstNonEmpty(os.Getenv("GOOGLE_CLOUD_LOCATION"), "us-central1")
		if project == "" {
			return nil, fmt.Errorf("GOOGLE_CLOUD_PROJECT is required with AGENT_ENGINE_ID")
		}
		log.Printf("using Agent Engine %s in %s/%s", id, project, location)
		return newAgentEngineClient(ctx, project, location, id, appName)
	}
	base := strings.TrimRight(firstNonEmpty(os.Getenv("AGENT_URL"), "http://localhost:8080/api"), "/")
	log.Printf("using local agent at %s", base)
	return &adkrestClient{base: base, appName: appName, http: &http.Client{Timeout: 3 * time.Minute}}, nil
}

func summariseHandler(client AgentClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req summariseRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, summariseResponse{Error: "invalid request: " + err.Error()})
			return
		}
		reply, sessionID, err := client.Summarise(r.Context(), "web-user", req.SessionID, buildMessage(req))
		if err != nil {
			log.Printf("summarise: %v", err)
			writeJSON(w, http.StatusBadGateway, summariseResponse{SessionID: sessionID, Error: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, summariseResponse{Reply: reply, SessionID: sessionID})
	}
}

func buildMessage(req summariseRequest) string {
	var b strings.Builder
	b.WriteString("Summarise the Twitch chat")
	ch := strings.TrimSpace(req.Channel)
	if req.Stored {
		b.WriteString(" from the stored messages (source: stored)")
		if ch != "" {
			fmt.Fprintf(&b, " in %s", ch)
		}
	} else if ch != "" {
		fmt.Fprintf(&b, " for channel %s", ch)
	}
	if req.Seconds > 0 && !req.Stored {
		fmt.Fprintf(&b, " over %d seconds", req.Seconds)
	}
	if req.Messages > 0 {
		fmt.Fprintf(&b, ", up to %d messages", req.Messages)
	}
	b.WriteString(".")
	return b.String()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
