package vapor

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"io"

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
	SetSourceJobID(uint64)
	GetTargetJobID() uint64
	SetConnectionInfo(authConnectionInfo)
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
	emsgFactory, ok := emsgToCMsg[emsg]
	if !ok {
		return nil, ErrNoProtoForEMsg
	}
	msg := emsgFactory()
	err := proto.Unmarshal(data, msg)
	if err != nil {
		return nil, err
	}
	return msg, nil
}

func NewExtendedMsgHeaderFromBytes(data []byte) *extendedMsgHeader {
	return &extendedMsgHeader{
		emsg:          EMsg(binary.LittleEndian.Uint32(data[:4])),
		headerSize:    uint8(data[4]), // This should always be 36
		headerVersion: binary.LittleEndian.Uint16(data[5:7]),
		targetJobID:   binary.LittleEndian.Uint64(data[7:15]),
		sourceJobID:   binary.LittleEndian.Uint64(data[15:23]),
		headerCanary:  data[23],
		steamID:       binary.LittleEndian.Uint64(data[24:32]),
		sessionID:     int32(binary.LittleEndian.Uint32(data[32:])),
	}
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

func (h *msgHeader) SetSourceJobID(val uint64) {
	h.sourceJobID = val
}

func (h *msgHeader) GetTargetJobID() uint64 {
	return h.targetJobID
}

func (h *msgHeader) SetConnectionInfo(info authConnectionInfo) {
	return
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

func (h *msgHeaderPB) SetSourceJobID(val uint64) {
	h.header.JobidSource = &val
}

func (h *msgHeaderPB) GetTargetJobID() uint64 {
	return h.header.GetJobidTarget()
}

func (h *msgHeaderPB) SetConnectionInfo(info authConnectionInfo) {
	h.header.Steamid = proto.Uint64(info.SteamID)
	h.header.ClientSessionid = proto.Int32(info.ClientSessionID)
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

func (h *extendedMsgHeader) SetSourceJobID(val uint64) {
	h.sourceJobID = val
}

func (h *extendedMsgHeader) GetTargetJobID() uint64 {
	return h.targetJobID
}

func (h *extendedMsgHeader) SetConnectionInfo(info authConnectionInfo) {
	h.steamID = info.SteamID
	h.sessionID = info.ClientSessionID
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
	_, err = io.ReadFull(zr, decompressedData)
	if err != nil {
		return nil, err
	}
	return decompressedData, nil
}

func parseMsgHeader(data []byte) (*msgHeader, error) {
	// The minimum length of a MsgHdr is 20 bytes
	if len(data) < msgHeaderMinSizeBytes {
		return nil, ErrMalformedPacket
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
	return &header, nil
}
