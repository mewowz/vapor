package vapor

import (
	"testing"
	"net"
	"encoding/binary"
	"bytes"
	"bufio"
	"errors"
	"crypto/rand"

	"github.com/google/go-cmp/cmp"
)


func TestGetRawPayload(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	steamConn := SteamConnection{}
	steamConn.conn = client
	steamConn.connReader = bufio.NewReader(client)

	tests := []struct {
		srvPayloadLen 	uint32
		srvMagicPacket 	uint32
		srvPayload    	[]byte
		want			[]byte
		wantErr			error
	}{
		{
			3,
			MagicPacket,
			[]byte{1,2,3},
			[]byte{1,2,3},
			nil,
		},
		{
			0,
			0x01010101,
			[]byte{},
			nil,
			ErrBadMagic,
		},
	}

	sendPayload := func(payloadLen, magic uint32, payload []byte) {
		var data []byte
		data = binary.LittleEndian.AppendUint32(data, payloadLen)
		data = binary.LittleEndian.AppendUint32(data, magic)
		data = append(data, payload...)
		server.Write(data)
	}

	for _, test := range tests {
		go sendPayload(test.srvPayloadLen, test.srvMagicPacket, test.srvPayload)

		got, gotErr := steamConn.getRawPayload()
		if !bytes.Equal(got, test.want) {
			t.Errorf(
				"SteamConnection.getRawPayload() = %v, %v, want %v, %v",
				got, gotErr, test.want, test.wantErr,
			)
		}
		if !errors.Is(gotErr, test.wantErr) {
			t.Errorf(
				"SteamConnection.getRawPayload() = %v, %v, want %v, %v",
				got, gotErr, test.want, test.wantErr,
			)
		}
	}

}

func TestParseMsgHeader(t *testing.T) {
	getMockMsgHeader := func(EMsg int32, TargetJobID, SourceJobID uint64, Body []byte) msgHeader {
		return msgHeader {
			EMsg,
			TargetJobID,
			SourceJobID,
			Body,
		}
	}
	getMockMsgHeaderPayload := func(EMsg int32, TargetJobID, SourceJobID uint64, Body []byte) []byte {
		data := []byte{}
		data = binary.LittleEndian.AppendUint32(data, uint32(EMsg))
		data = binary.LittleEndian.AppendUint64(data, TargetJobID)
		data = binary.LittleEndian.AppendUint64(data, SourceJobID)
		data = append(data, Body...)
		return data
	}
	tests := []struct {
		input	[]byte
		want	msgHeader
		wantErr	error
	}{
		{
			getMockMsgHeaderPayload(1, 1, 1, []byte{1,1,1}),
			getMockMsgHeader(1, 1, 1, []byte{1,1,1}),
			nil,
		},
		{
			getMockMsgHeaderPayload(1, 1, 1, []byte{}),
			getMockMsgHeader(1, 1, 1, []byte{}),
			nil,
		},
		{
			[]byte{1, 1},
			msgHeader{},
			ErrMalformedPacket,
		},
	}
	for _, test := range tests {
		got, gotErr := parseMsgHeader(test.input)
		if !cmp.Equal(got, test.want) {
			t.Errorf(
				"parseMsgHeader(%v) = %v, %v, want %v, %v",
				test.input, got, gotErr, test.want, test.wantErr,
			)
		}
		if !errors.Is(gotErr, test.wantErr) {
			t.Errorf(
				"parseMsgHeader(%v) = %v, %v, want %v, %v",
				test.input, got, gotErr, test.want, test.wantErr,
			)
		}
	}
}

// TestHMACEncryptionAndDecryption will test the round-trip encryption and decryption
// for an HMAC filter
func TestHMACEncryptionAndDecryption(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)
	filter := NewHMACFilter(key)

	rawMsg := []byte("12345")
	encMsg, err := filter.EncryptMessage(rawMsg)
	if err != nil {
		t.Fatalf(
			"filter.EncryptMessage(%v) = %v, err = %v, want err = nil",
			rawMsg, encMsg, err,
		)
	}
	decMsg, err := filter.DecryptMessage(encMsg)
	if err != nil {
		t.Fatalf(
			"filter.DecryptMessage(%v) = %v, err = %v, want err = nil",
			rawMsg, decMsg, err,
		)
	}

	if !bytes.Equal(decMsg, rawMsg) {
		t.Fatalf(
			"decMsg = %v != rawMsg = %v, want decMsg == rawMsg",
			decMsg, rawMsg,
		)
	}

}
