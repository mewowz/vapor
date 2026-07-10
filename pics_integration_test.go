//go:build integration

package vapor

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestRequestProductInfoAnonymous(t *testing.T) {
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	logger := slog.New(handler)
	steamConn := NewSteamConnection(10*time.Second, logger)
	err := steamConn.CMConnect(10 * time.Second)
	if err != nil {
		t.Fatalf("CMConnect failed: %v", err)
	}
	defer func() {
		steamConn.netLoopCancel(context.Canceled)
		select {
		case <-steamConn.netLoopCtx.Done():
		case <-time.After(2 * time.Second):
			t.Fatalf("netloop: context is not Done()")
		}
	}()
	defer steamConn.conn.Close()

	err = steamConn.StartNetLoop()
	if err != nil {
		t.Fatalf("StartNetLoop failed: %v", err)
	}

	auth := NewAnonymousAuthenticator(steamConn)

	err = auth.Logon()
	if err != nil {
		t.Fatalf("auth.Logon failed: %v", err)
	}

	appInfos, _, err := RequestProductInfo(
		[]uint32{730},
		[]uint32{},
		steamConn,
		logger,
		auth.connInfo,
	)
	if err != nil {
		t.Fatalf("RequestProductInfo failed: %v", err)
	}
	t.Logf("Got appinfos: %v", appInfos)
}
