package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arunima10a/go-ws-chat/internal/chat"
	"github.com/gorilla/websocket"
)

func TestHandshake(t *testing.T) {
	hub := chat.NewHub(nil, nil)
	go hub.Run()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveWs(hub, w, r)
	}))
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")

	_, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Handshake failed: %v", err)
	}
}
