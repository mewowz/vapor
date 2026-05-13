package vapor

import (
	"testing"
	"net"
	"encoding/binary"
	"bytes"
	"bufio"
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
		if test.wantErr != gotErr {
			t.Errorf(
				"SteamConnection.getRawPayload() = %v, %v, want %v, %v",
				got, gotErr, test.want, test.wantErr,
			)
		}
	}

}
