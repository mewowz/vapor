//go:build integration

package vapor

import (
	"encoding/binary"
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestGetDepotDecryptionKey(t *testing.T) {
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	logger := slog.New(handler)
	client, err := NewSteamClient(WithLogger(logger), WithTimeout(10*time.Second))

	if err = client.Start(); err != nil {
		t.Fatalf("client.Connect() failed: %v", err)
	}
	if err = client.LogonAnonymous(); err != nil {
		t.Fatalf("client.Connect() failed: %v", err)
	}

	key, err := GetDepotDecryptionKey(
		2347770, // Pulled from steamdb.info
		730,
		client.connection,
		logger,
	)
	if err != nil {
		t.Fatalf("GetDeoptDecryptionKey failed: %v", err)
	}
	if key == nil || binary.LittleEndian.Uint32(key) == 0 {
		t.Fatalf("GetDepotDecryptionKey got empty/zeroed key")
	}
}

func TestGetServersForSteamPipe(t *testing.T) {
	// Its believed that the cellID that Steam supplies is clamped or
	// handled in a round-robin due to being unable to find
	// an "invalid" value for cellID within heuristic testing
	cellID := uint32(123456789)
	_, err := GetServersForSteamPipe(cellID, DefaultDialTimeoutSeconds*time.Second)
	if err != nil {
		t.Fatalf("GetServersForSteamPipe failed: %v", err)
	}
}
