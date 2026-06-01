//go:build integration

package vapor

import (
	"bufio"
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestEstablishEncryptedChannel(t *testing.T) {
	var err error
	host := "162.254.192.101:27017"
	steamConn := NewSteamConnection(5 * time.Second)
	dialer := net.Dialer{}
	steamConn.conn, err = dialer.DialContext(context.Background(), "tcp", host)
	if err != nil {
		t.Fatalf("Could not connect to Steam CM server: %v", err)
	}
	defer steamConn.conn.Close()
	steamConn.connReader = bufio.NewReader(steamConn.conn)

	success, err := steamConn.establishEncryptedChannel()
	if !success {
		t.Errorf("establishEncryptedChannel() = %v, %v", success, err)
	}
}

func TestConnectToCMServerTCP(t *testing.T) {
	host := "162.254.192.101:27017"
	timeoutSeconds := 10 * time.Second
	steamConn := NewSteamConnection(timeoutSeconds)
	//defer steamConn.conn.Close()
	err := steamConn.connectToCMServerTCP(host, timeoutSeconds)
	if err != nil {
		t.Errorf(
			"steamConn.connectToCMServerTCP(%v, %v) = %v, want %v",
			host, timeoutSeconds, err, nil,
		)
	}
	steamConn.conn.Close()
}

func TestGetCMServerHost(t *testing.T) {
	timeoutSeconds := 10 * time.Second
	host, err := getCMServerHost(timeoutSeconds)
	if err != nil {
		t.Errorf(
			"getCMServerHost(%v) = %v, %v, want <json>, %v",
			timeoutSeconds, host, err, nil,
		)
	}
}

func TestCMConnect(t *testing.T) {
	timeoutSeconds := 10 * time.Second
	steamConn := NewSteamConnection(timeoutSeconds)
	err := steamConn.CMConnect(timeoutSeconds)
	if err != nil {
		t.Errorf(
			"steamConn.CMConnect(%v) = %v, want %v",
			timeoutSeconds, err, nil,
		)
	}

	// Test an impossibly low timeout
	timeoutInstant := 1 * time.Microsecond
	steamConn = NewSteamConnection(timeoutInstant)
	err = steamConn.CMConnect(timeoutInstant)
	if timeoutOpErr, ok := errors.AsType[*net.OpError](err); ok {
		if !timeoutOpErr.Timeout() {
			t.Errorf(
				"steamConn.CMConnect(%v) = %v, want %v",
				timeoutInstant, err, net.OpError{},
			)
		}
	}

	steamConn.connState = Connected
	err = steamConn.CMConnect(timeoutInstant)
	if !errors.Is(err, ErrAlreadyConnectedToCM) {
		t.Errorf(
			"with steamConn.connState != Disconnected: steamConn.CMConnect(%v) = %v, want %v",
			timeoutInstant, err, ErrAlreadyConnectedToCM,
		)
	}
}
