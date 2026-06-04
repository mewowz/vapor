//go:build integration

package vapor

import (
	"context"
	"testing"
	"time"
)

func TestAnonymousLogon(t *testing.T) {
	steamConn := NewSteamConnection(10*time.Second, nil)
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
}
