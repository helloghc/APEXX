package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClaudeAgainstFakeServer(t *testing.T) {
	var gotKey, gotPath string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey, gotPath = r.Header.Get("Authorization"), r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "msg_1", "type": "message", "role": "assistant", "model": "claude-haiku-5-5",
			"content": []map[string]any{{"type": "text", "text": "hola"}}, "stop_reason": "end_turn",
			"usage": map[string]any{"input_tokens": 1000, "output_tokens": 100}})
	}))
	defer fake.Close()
	c := NewClaude("k-test", fake.URL, "claude-haiku-5-5")
	text, u, err := c.Complete(context.Background(), Prompt{System: "s", User: "u"})
	if err != nil || text != "hola" {
		t.Fatalf("%q %v", text, err)
	}
	if gotKey != "Bearer k-test" || gotPath != "/v1/messages" {
		t.Fatalf("key %q path %q", gotKey, gotPath)
	}
	// 1000 in × 0.10 USD/M + 100 out × 0.50 USD/M = 150 micro-dollars
	if u.CostMicros != 150 || u.Tokens != 1100 {
		t.Fatalf("usage %+v", u)
	}
}
