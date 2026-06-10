package vapor

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"

	"github.com/mewowz/vapor/internal/steamproto"
	"google.golang.org/protobuf/proto"
)

type HeaderType int

const (
	GenericHeader HeaderType = iota
	ExtendedHeader
	ProtoHeader
)

type Message interface {
	EMsg() EMsg
	HeaderType() HeaderType
	Body() ([]byte, error)
	Bytes() ([]byte, error)
	Proto() proto.Message
}

type connectionHeader struct {
	PayloadLen uint32
	Magic      uint32
}

type msgHeader struct {
	emsg        EMsg
	targetJobID uint64
	sourceJobID uint64
	body        []byte
}

type extendedMsgHeader struct {
	emsg          EMsg
	headerSize    uint8
	headerVersion uint16
	targetJobID   uint64
	sourceJobID   uint64
	headerCanary  uint8
	steamID       uint64
	sessionID     int32
}

type msgHeaderPB struct {
	emsg   EMsg
	header *steamproto.CMsgProtoBufHeader
	body   proto.Message
}

func NewMsgHeaderPB(emsg EMsg, pbBody proto.Message) (*msgHeaderPB, error) {
	pbHeader := steamproto.CMsgProtoBufHeader{}
	return &msgHeaderPB{
		EMsg(uint32(emsg) | 0x80000000),
		&pbHeader,
		pbBody,
	}, nil
}

// NewMsgHeaderPBFromBytes creates a msgHeaderPB from the wire format and into
// a usable msgHeaderPB.
func NewMsgHeaderPBFromBytes(data []byte) (*msgHeaderPB, error) {
	rawEMsg := binary.LittleEndian.Uint32(data[:4])
	emsg := EMsg(rawEMsg & 0x7FFFFFFF)

	headerSizeBytes := binary.LittleEndian.Uint32(data[4:8])
	pbHeader := steamproto.CMsgProtoBufHeader{}
	err := proto.Unmarshal(data[8:8+headerSizeBytes], &pbHeader)
	if err != nil {
		return nil, err
	}

	pbBody, err := NewPBFromEMsg(emsg, data[8+headerSizeBytes:])
	if err != nil {
		return nil, err
	}
	return &msgHeaderPB{
		emsg,
		&pbHeader,
		pbBody,
	}, nil
}

func NewPBFromEMsg(emsg EMsg, data []byte) (proto.Message, error) {
	switch emsg {
	case EMsgMulti:
		var msg steamproto.CMsgMulti
		err := proto.Unmarshal(data, &msg)
		return &msg, err
	case EMsgClientHello:
		var msg steamproto.CMsgClientHello
		err := proto.Unmarshal(data, &msg)
		return &msg, err
	case EMsgClientLogon:
		var msg steamproto.CMsgClientLogon
		err := proto.Unmarshal(data, &msg)
		return &msg, err
	case EMsgClientLogOnResponse:
		var msg steamproto.CMsgClientLogonResponse
		err := proto.Unmarshal(data, &msg)
		return &msg, err
	case EMsgClientLicenseList:
		var msg steamproto.CMsgClientLicenseList
		err := proto.Unmarshal(data, &msg)
		return &msg, err
	case EMsgClientHeartBeat:
		var msg steamproto.CMsgClientHeartBeat
		err := proto.Unmarshal(data, &msg)
		return &msg, err
	}
	return nil, ErrNoProtoForEMsg
}

func newConnectionHeader(payloadLen uint32) *connectionHeader {
	return &connectionHeader{
		payloadLen,
		MagicPacket,
	}
}

func (h *msgHeader) EMsg() EMsg {
	return h.emsg
}

func (h *msgHeader) HeaderType() HeaderType {
	return GenericHeader
}

func (h *msgHeader) Body() ([]byte, error) {
	return h.body, nil
}

func (h *msgHeader) Proto() proto.Message {
	panic("call to Proto() on non-protobuf Message")
}

func (h *msgHeader) Size() int {
	return int(binary.Size(h.emsg) +
		binary.Size(h.targetJobID) +
		binary.Size(h.sourceJobID) +
		len(h.body))
}

func (h *msgHeader) Bytes() ([]byte, error) {
	fullBytes := make([]byte, msgHeaderMinSizeBytes)
	bodyBytes, _ := h.Body()
	binary.LittleEndian.PutUint32(fullBytes[:4], uint32(h.emsg))
	copy(fullBytes[4:], bodyBytes)
	return fullBytes, nil
}

// Bytes will return the header in wire-format using Little-Endian encoding.
// Bytes will also ensure that the EMsg has the correct bit set to indicate that it is
// a protobuf message.
func (h *msgHeaderPB) Bytes() ([]byte, error) {
	headerBytes, err := proto.Marshal(h.header)
	if err != nil {
		return nil, err
	}
	var data []byte
	data = binary.LittleEndian.AppendUint32(data, uint32(h.emsg)|0x80000000)
	data = binary.LittleEndian.AppendUint32(data, uint32(len(headerBytes)))
	data = append(data, headerBytes...)
	bodyBytes, err := proto.Marshal(h.body)
	if err != nil {
		return nil, err
	}
	data = append(data, bodyBytes...)
	return data, nil
}

func (h *msgHeaderPB) EMsg() EMsg {
	return h.emsg
}

func (h *msgHeaderPB) HeaderType() HeaderType {
	return ProtoHeader
}

func (h *msgHeaderPB) Body() ([]byte, error) {
	bodyBytes, err := proto.Marshal(h.body)
	return bodyBytes, err
}

func (h *msgHeaderPB) Proto() proto.Message {
	return h.body
}

func (h *msgHeaderPB) ProtoHeader() *steamproto.CMsgProtoBufHeader {
	return h.header
}

func (h *extendedMsgHeader) EMsg() EMsg {
	return h.emsg
}

func (h *extendedMsgHeader) HeaderType() HeaderType {
	return ExtendedHeader
}

// Body will return all fields byte-encoded from extendedMsgHeader EXCEPT:
// EMsg, HeaderSize, HeaderVersion
func (h *extendedMsgHeader) Body() ([]byte, error) {
	var bodyBytes []byte
	bodyBytes = binary.LittleEndian.AppendUint64(bodyBytes, h.targetJobID)
	bodyBytes = binary.LittleEndian.AppendUint64(bodyBytes, h.sourceJobID)
	bodyBytes = append(bodyBytes, h.headerCanary)
	bodyBytes = binary.LittleEndian.AppendUint64(bodyBytes, h.steamID)
	bodyBytes = binary.LittleEndian.AppendUint32(bodyBytes, uint32(h.sessionID))
	return bodyBytes, nil
}

func (h *extendedMsgHeader) Bytes() ([]byte, error) {
	var fullBytes []byte
	fullBytes = binary.LittleEndian.AppendUint32(fullBytes, uint32(h.emsg))
	fullBytes = append(fullBytes, h.headerSize)
	fullBytes = binary.LittleEndian.AppendUint16(fullBytes, h.headerVersion)
	bodyBytes, _ := h.Body()
	fullBytes = append(fullBytes, bodyBytes...)
	return fullBytes, nil
}

func UnpackCMsgMultiToBytes(msg *steamproto.CMsgMulti) ([][]byte, error) {
	var rawBody []byte
	if msg.GetSizeUnzipped() > 0 {
		decompressed, err := gzipDecompress(msg.GetMessageBody(), msg.GetSizeUnzipped())
		if err != nil {
			return nil, err
		}
		rawBody = decompressed
	} else {
		rawBody = msg.GetMessageBody()
	}
	var msgsBytes [][]byte
	for len(rawBody) > 0 {
		subSize := binary.LittleEndian.Uint32(rawBody[:4])
		subData := rawBody[4 : 4+subSize]
		msgsBytes = append(msgsBytes, subData)

		rawBody = rawBody[4+subSize:]
	}
	return msgsBytes, nil
}

func (h *extendedMsgHeader) Proto() proto.Message {
	panic("call to Proto() on non-protobuf Message")
}

func gzipDecompress(data []byte, unzippedSize uint32) ([]byte, error) {
	r := bytes.NewReader(data)
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	decompressedData := make([]byte, unzippedSize)
	_, err = zr.Read(decompressedData)
	if err != nil {
		return nil, err
	}
	return decompressedData, nil
}

func parseMsgHeader(data []byte) (msgHeader, error) {
	// The minimum length of a MsgHdr is 20 bytes
	if len(data) < msgHeaderMinSizeBytes {
		return msgHeader{}, ErrMalformedPacket
	}
	var header msgHeader

	header.emsg = EMsg(binary.LittleEndian.Uint32(data[0:4]))
	header.targetJobID = binary.LittleEndian.Uint64(data[4:12])
	header.sourceJobID = binary.LittleEndian.Uint64(data[12:20])

	// Then just read whatever is left into the body, if anything
	if len(data) > msgHeaderMinSizeBytes {
		header.body = data[msgHeaderMinSizeBytes:]
	} else {
		header.body = []byte{}
	}
	return header, nil
}
