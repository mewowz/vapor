//go:build integration

package vapor

import (
	"testing"
	"context"
	"bufio"
)

func TestEstablishEncryptedChannel(t *testing.T) {
	var err error
	steamConn := SteamConnection{}
	steamConn.conn, err = steamConn.dialer.DialContext(context.Background(), "tcp", "162.254.192.101:27017")
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
