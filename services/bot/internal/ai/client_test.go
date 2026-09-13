package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEmbedText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("unexpected authorization: %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["input"] != "кот грустит" {
			t.Fatalf("unexpected input: %#v", payload["input"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[3,4]}]}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "secret", "model", 2)
	vector, err := client.EmbedText(context.Background(), "кот грустит")
	if err != nil {
		t.Fatal(err)
	}
	if vector[0] != 0.6 || vector[1] != 0.8 {
		t.Fatalf("vector was not normalized: %#v", vector)
	}
}
