package ai

import (
	"context"
	"encoding/json"
	"net"
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

// The cluster's IPv6 egress blackholes, so the transport must not hand a
// request to it and wait out the dial timeout.
func TestTransportPrefersIPv4(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	transport := newTransport()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	conn, err := transport.DialContext(context.Background(), "tcp", "localhost:"+port)
	if err != nil {
		t.Fatalf("dial should reach the IPv4 listener: %v", err)
	}
	defer conn.Close()
	if addr, ok := conn.RemoteAddr().(*net.TCPAddr); !ok || addr.IP.To4() == nil {
		t.Fatalf("expected an IPv4 connection, got %v", conn.RemoteAddr())
	}
}
