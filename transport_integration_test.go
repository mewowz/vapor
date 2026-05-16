//go:build integration

package vapor

import (
	"testing"
	"context"
	"bufio"
)

func TestEstablishEncryptedChannel(t *testing.T) {
	var err error
	host := "162.254.192.101:27017"
	steamConn := SteamConnection{}
	steamConn.conn, err = steamConn.dialer.DialContext(context.Background(), "tcp", host)
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
	steamConn := SteamConnection{ connContext: context.Background() }
	//defer steamConn.conn.Close()
	success, err := steamConn.connectToCMServerTCP(host)
	if !success {
		t.Errorf(
			"steamConn.connectToCMServerTCP(%v) = %v, %v, want %v, %v",
			host, success, err, true, nil,
		)
	}
	steamConn.conn.Close()
}
