package rediscache_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"urlshortener/internal/repository/rediscache"
)

func TestNewClient_Success(t *testing.T) {
	mr := miniredis.RunT(t)

	client, err := rediscache.NewClient(context.Background(), mr.Addr())
	if err != nil {
		t.Fatalf("NewClient() unexpected error: %v", err)
	}
	defer client.Close()
}

func TestNewClient_PingFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Nothing listens on this port — Ping should fail, not hang.
	_, err := rediscache.NewClient(ctx, "127.0.0.1:1")
	if err == nil {
		t.Fatal("NewClient() error = nil, want non-nil for an unreachable address")
	}
}
