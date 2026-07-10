//go:build integration

package vapor

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestAnonymousLogon(t *testing.T) {
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
}

func TestPasswordLogonNo2FA(t *testing.T) {
	username := os.Getenv("TEST_STEAM_LOGIN_USERNAME")
	password := os.Getenv("TEST_STEAM_LOGIN_PASSWORD")

	if username == "" || password == "" {
		t.Skip(
			"TEST_STEAM_LOGIN_USERNAME / TEST_STEAM_LOGIN_PASSWORD not set; skipping", t.Name(),
		)
	}

	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	logger := slog.New(handler)
	logger.Debug("sending credentials", "username", username, "password", password)
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

	auth := NewAuthenticator(steamConn)
	credentials := LogonCredentials{username: username, password: password}
	err = auth.Logon(&credentials)
	if err != nil {
		t.Fatalf("auth.Logon failed: %v", err)
	}
}
