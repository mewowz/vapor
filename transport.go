package "vapor"

import (
	"net"
	"context"
	"sync"
	"time"
	"bufio"
	"io"
	"encoding/binary"
	"bytes"
)


const magicPacket = "VT01"
const channelEncryptRequestMinSize = 8

type ConnectionState int
const (
	Disconnected ConnectionState iota
	Connected
	Challenged
	Encrypted
)

type SteamConnection struct {
	connDeadline	time.Time
	connState		ConnectionState
	connContext		context.Context
	conn			net.Conn
	dialer			net.Dialer
	connReader		*bufio.Reader
	readMut			sync.Mutex
}

type connectionHeader struct {
	payloadLen	uint32
	magic		uint32
}

const msgHdrSize = 20
type msgHeader struct {
	EMsg		int32
	TargetJobID	uint64
	SourceJobID	uint64
	Body		[]byte
}

func (c *SteamConnection) connectToCMServerTCP(cmHost string) (bool, error) {
	network := "tcp"

	c.conn, err := c.dialer.DialContext(c.connContext, network, cmHost)
	if err != nil {
		return false, err
	}
	c.connState = Connected

	c.connReader = bufio.NewReader(conn)

	success, err := c.establishEncryptedChannel()
	if success {
		c.connState = Encrypted
	}

	return success, err
}

func (c *SteamConnection) netLoop() error {
	for {
		data, err := c.getPayload()
		if err != nil {
			return err
		}
	}
}

func (c *SteamConnection) getRawPayload() ([]byte, error) {
	var header connectionHeader
	err := binary.Read(c.connReader, binary.LittleEndian, &connectionHeader)
	if err != nil {
		return err
	}

	if header.magic != magicPacket {
		return nil, ErrBadMagic
	}

	payload := make([]byte, header.payloadLen)
	_, err := io.ReadFull(c.connReader, payload)
	if err != nil {
		return nil, err
	}

	return payload, nil

}

func (c *SteamConnection) getPayload() ([]byte, error) {
}

func (c *SteamConnection) establishEncryptedChannel() (bool, error) {
	payload, err := c.getRawPayload()
	if err != nil {
		return false, err
	}

	// The payload should just be the ChannelEncryptRequest
	header, err := getMsgHeader(payload)
	if err != nil {
		return false, err
	}
	if len(header) < channelEncryptRequestMinSize {
		return false, ErrBadChannelEncryptRequest
	}

	protocolVersion := binary.LittleEndian.Uint32(header.Body[:4])
	universe := binary.LittleEndian.Uint32(header.Body[4:8])
	randomChallenge := new([]byte, len(header.Body[8:]))
	err := binary.Decode(header.Body[8:], binary.LittleEndian, &randomChallenge)
	if err != nil {
		return false, err
	}

}

func getMsgHeader(data []byte) (msgHeader, error) {
	// The minimum length of a MsgHdr is 20 bytes
	// This assumes that the MsgHdr has no body accompanying it
	if len(data) < msgHdrSize {
		return msgHeader{}, ErrMalformedPacket
	}
	var header msgHeader

	header.EMsg = binary.LittleEndian.Uint32(data[0:4])
	header.TargetJobID = binary.LittleEndian.Int64(data[4:12])
	header.SourceJobID = binary.LittleEndian.Int64(data[12:20])

	// Then just read whatever is left into the body, if anything
	if len(data) > msgHdrSize {
		header.Body = data[msgHdrSize:]
	}
	return header, nil
}
