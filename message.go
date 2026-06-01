package vapor

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"

	"github.com/mewowz/vapor/internal/steamproto"
	"google.golang.org/protobuf/proto"
)

type connectionHeader struct {
	PayloadLen uint32
	Magic      uint32
}

type msgHeader struct {
	EMsg        EMsg
	TargetJobID uint64
	SourceJobID uint64
	Body        []byte
}

type msgHeaderPB struct {
	EMsg   EMsg
	Header steamproto.CMsgProtoBufHeader
	Body   proto.Message
}

func NewMsgHeaderPB(emsg EMsg, pbBody proto.Message) (*msgHeaderPB, error) {
	pbHeader := steamproto.CMsgProtoBufHeader{}
	return &msgHeaderPB{
		EMsg(uint32(emsg) | 0x80000000),
		pbHeader,
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
		pbHeader,
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

// Bytes will return the header in wire-format using Little-Endian encoding.
// Bytes will also ensure that the EMsg has the correct bit set to indicate that it is
// a protobuf message.
func (h *msgHeaderPB) Bytes() ([]byte, error) {
	headerBytes, err := proto.Marshal(&h.Header)
	if err != nil {
		return nil, err
	}
	var data []byte
	data = binary.LittleEndian.AppendUint32(data, uint32(h.EMsg)|0x80000000)
	data = binary.LittleEndian.AppendUint32(data, uint32(len(headerBytes)))
	data = append(data, headerBytes...)
	bodyBytes, err := proto.Marshal(h.Body)
	if err != nil {
		return nil, err
	}
	data = append(data, bodyBytes...)
	return data, nil
}

func (h *msgHeader) Size() uint {
	return uint(binary.Size(h.EMsg) +
		binary.Size(h.TargetJobID) +
		binary.Size(h.SourceJobID) +
		len(h.Body))
}

func UnpackCMsgMultiToBytes(msg steamproto.CMsgMulti) ([][]byte, error) {
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

	header.EMsg = EMsg(binary.LittleEndian.Uint32(data[0:4]))
	header.TargetJobID = binary.LittleEndian.Uint64(data[4:12])
	header.SourceJobID = binary.LittleEndian.Uint64(data[12:20])

	// Then just read whatever is left into the body, if anything
	if len(data) > msgHeaderMinSizeBytes {
		header.Body = data[msgHeaderMinSizeBytes:]
	} else {
		header.Body = []byte{}
	}
	return header, nil
}
