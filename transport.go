package vapor

import (
	"net"
	"context"
	"sync"
	"time"
	"bufio"
	"io"
	"encoding/binary"
	"crypto/x509"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"math"
	"hash/crc32"
	"crypto/aes"
	"crypto/hmac"
	"crypto/cipher"
	"bytes"

	"google.golang.org/protobuf/proto"
	"github.com/andreburgaud/crypt2go/ecb"
	"github.com/mewowz/vapor/internal/steamproto"
)

const (
	EMsgChannelEncryptRequest 	int32 = 1303
	EMsgChannelEncryptResponse	int32 = 1304
	EMsgChannelEncryptResult	int32 = 1305

	EMsgClientHello				int32 = 858
)

const MagicPacket uint32 = 0x31305456 // VT01
const ChannelEncryptRequestMinSize uint = 8

const DefaultJobID uint64 = math.MaxUint64

// This is following SteamKit's MsgClientLogon.CurrentProtocol
const CurrentProtocolVersion uint32 = 65581 

type ConnectionState int
const (
	Disconnected ConnectionState = iota
	Connected
	Challenged
	Encrypted
)

type HMACFilter struct {
	HMACSecret	[]byte
	AESKey		[]byte
}

type SteamConnection struct {
	connDeadline	time.Time
	connState		ConnectionState
	connContext		context.Context
	conn			net.Conn
	dialer			net.Dialer
	connReader		*bufio.Reader
	encFilter		*HMACFilter
	readMut			sync.Mutex
	writeMut		sync.Mutex
}

type connectionHeader struct {
	PayloadLen	uint32
	Magic		uint32
}

const msgHeaderMinSizeBytes = 20
type msgHeader struct {
	EMsg		int32
	TargetJobID	uint64
	SourceJobID	uint64
	Body		[]byte
}

type msgHeaderProtoBuf struct {
	EMsg		int32
	HeaderLen	uint32
	Header		steamproto.CMsgProtoBufHeader
	Body		proto.Message
}

func NewHMACFilter(sessionKey []byte) *HMACFilter {
	return &HMACFilter{
		HMACSecret: sessionKey[:16],
		AESKey:		sessionKey[:32],
	}
}

func (c *SteamConnection) connectToCMServerTCP(cmHost string) (bool, error) {
	var err error
	network := "tcp"

	c.conn, err = c.dialer.DialContext(c.connContext, network, cmHost)
	if err != nil {
		return false, err
	}
	c.connState = Connected

	c.connReader = bufio.NewReader(c.conn)

	encryptSuccess, err := c.establishEncryptedChannel()
	if encryptSuccess {
		c.connState = Encrypted
	} else {
		return false, err
	}

	clientHello := steamproto.CMsgClientHello{ ProtocolVersion: proto.Uint32(CurrentProtocolVersion) }
	header, err := NewMessageHeaderProtoBuf(EMsgClientHello, &clientHello)
	if err != nil {
		return false, err
	}
	headerBytes, err := header.Marshal()
	if err != nil {
		return false, err
	}
	err = c.sendPayload(headerBytes)
	if err != nil {
		return false, err
	}

	return true, err
}

func NewMessageHeaderProtoBuf(EMsg int32, Body proto.Message) (*msgHeaderProtoBuf, error) {
	header := steamproto.CMsgProtoBufHeader{}
	headerBytes, err := proto.Marshal(&header)
	if err != nil {
		return nil, err
	}
	headerSizeBytes := uint32(len(headerBytes))
	return &msgHeaderProtoBuf{
		EMsg,
		headerSizeBytes,
		header,
		Body,
	}, nil
}

// Marshal will return the header in wire-format using Little-Endian encoding
func (h *msgHeaderProtoBuf) Marshal() ([]byte, error) {
	var data []byte
	data = binary.LittleEndian.AppendUint32(data, uint32(h.EMsg) | 0x80000000)
	data = binary.LittleEndian.AppendUint32(data, h.HeaderLen)
	headerBytes, err := proto.Marshal(&h.Header)
	if err != nil {
		return nil, err
	}
	data = append(data, headerBytes...)
	bodyBytes, err := proto.Marshal(h.Body)
	if err != nil {
		return nil, err
	}
	data = append(data, bodyBytes...)
	return data, nil
}

//func (c *SteamConnection) netLoop() error {
//	for {
//		data, err := c.getPayload()
//		if err != nil {
//			return err
//		}
//	}
//
//	return nil
//}

func (c *SteamConnection) getRawPayload() ([]byte, error) {
	var header connectionHeader
	err := binary.Read(c.connReader, binary.LittleEndian, &header)
	if err != nil {
		return nil, err
	}

	if header.Magic != MagicPacket {
		return nil, ErrBadMagic
	}

	payload := make([]byte, header.PayloadLen)
	_, err = io.ReadFull(c.connReader, payload)
	if err != nil {
		return nil, err
	}

	return payload, nil

}

func (c *SteamConnection) getPayload() ([]byte, error) {
	rawPayload, err := c.getRawPayload()
	if err != nil {
		return nil, err
	}
	
	if c.connState == Encrypted {
		payload, err := c.encFilter.DecryptMessage(rawPayload)
		return payload, err
	} else {
		return rawPayload, nil
	}
}

func (c *SteamConnection) sendPayload(rawPayload []byte) error {
	if c.connState == Encrypted {
		payload, err := c.encFilter.EncryptMessage(rawPayload)
		if err != nil {
			return err
		}
		err = c.sendRawPayload(payload)
		return err
	} else {
		err := c.sendRawPayload(rawPayload)
		return err
	}
}

func (h *msgHeader) Size() uint {
	return uint(binary.Size(h.EMsg) +
		binary.Size(h.TargetJobID) +
		binary.Size(h.SourceJobID) +
		len(h.Body) )
}

func (c *SteamConnection) establishEncryptedChannel() (bool, error) {
	// TODO: defer a function for cancelling the connection to properly disconnect
	// the socket instead of leaving it open

	payload, err := c.getRawPayload()
	if err != nil {
		return false, err
	}

	// The payload should just be the ChannelEncryptRequest
	header, err := parseMsgHeader(payload)
	if err != nil {
		return false, err
	}
	if header.Size() < ChannelEncryptRequestMinSize {
		return false, ErrBadChannelEncryptRequest
	}
	if header.EMsg == 1303 {
		c.connState = Challenged
	} else {
		return false, ErrBadChannelEncryptRequest
	}


	tempSessionKey, channelEncryptResponseMsgHeader, err := newChannelEncryptResponse(header)
	if err != nil {
		return false, err
	}
	err = c.sendRawMsgHeader(channelEncryptResponseMsgHeader)
	if err != nil {
		return false, err
	}

	payload, err = c.getRawPayload()
	if err != nil {
		return false, err
	}

	header, err = parseMsgHeader(payload)
	if err != nil {
		return false, err
	}
	if header.EMsg != 1305 {
		return false, ErrBadChannelEncryptResult
	}

	EResult := binary.LittleEndian.Uint32(header.Body)
	if EResult != 1 {
		return false, ErrBadChannelEncryptResult
	}
	c.encFilter = NewHMACFilter(tempSessionKey)

	return true, nil
}

func newChannelEncryptResponse(incomingHeader msgHeader) ([]byte, msgHeader, error) {
	protocolVersion := binary.LittleEndian.Uint32(incomingHeader.Body[:4])
	universe := binary.LittleEndian.Uint32(incomingHeader.Body[4:8])

	randomChallenge := make([]byte, len(incomingHeader.Body[8:]))
	_, err := binary.Decode(incomingHeader.Body[8:], binary.LittleEndian, &randomChallenge)
	if err != nil {
		return nil, msgHeader{}, err
	}

	// These are RSA keys
	universePubKey, err := getUniversePubKey(universe)
	if err != nil {
		return nil, msgHeader{}, err
	}
	// Sanity check
	universePubKeyRSA, ok := universePubKey.(*rsa.PublicKey)
	if !ok {
		return nil, msgHeader{}, ErrBadPublicKey
	}

	tempSessionKey := make([]byte, 32)
	rand.Read(tempSessionKey)

	blob := append(tempSessionKey, randomChallenge...)
	rng := rand.Reader
	encryptedBlob, err := rsa.EncryptOAEP(sha1.New(), rng, universePubKeyRSA, blob, nil)
	if err != nil {
		return nil, msgHeader{}, err
	}

	keyCrc := crc32.ChecksumIEEE(encryptedBlob)

	var channelEncryptResponseBody []byte
	channelEncryptResponseBody = binary.LittleEndian.AppendUint32(channelEncryptResponseBody, protocolVersion)
	channelEncryptResponseBody = binary.LittleEndian.AppendUint32(channelEncryptResponseBody, 128)
	channelEncryptResponseBody = append(channelEncryptResponseBody, encryptedBlob...)
	channelEncryptResponseBody = binary.LittleEndian.AppendUint32(channelEncryptResponseBody, keyCrc)
	channelEncryptResponseBody = binary.LittleEndian.AppendUint32(channelEncryptResponseBody, 0)
	channelEncryptResponseMsgHeader := msgHeader{1304, DefaultJobID, DefaultJobID, channelEncryptResponseBody}

	return tempSessionKey, channelEncryptResponseMsgHeader, nil
}

func (c *SteamConnection) sendRawMsgHeader(header msgHeader) error {
	payload := make([]byte, header.Size())
	binary.LittleEndian.PutUint32(payload[:4], uint32(header.EMsg))
	binary.LittleEndian.PutUint64(payload[4:12], header.TargetJobID)
	binary.LittleEndian.PutUint64(payload[12:20], header.SourceJobID)
	copy(payload[20:], header.Body)

	err := c.sendRawPayload(payload)
	return err
}

func newConnectionHeader(payloadLen uint32) *connectionHeader {
	return &connectionHeader {
		payloadLen,
		MagicPacket,
	}
}

func (c *SteamConnection) sendRawPayload(payload []byte) error {
	header := newConnectionHeader(uint32(len(payload)))

	var data []byte
	data = binary.LittleEndian.AppendUint32(data, header.PayloadLen)
	data = binary.LittleEndian.AppendUint32(data, header.Magic)
	data = append(data, payload...)
	_, err := c.conn.Write(data)
	return err
}

func parseMsgHeader(data []byte) (msgHeader, error) {
	// The minimum length of a MsgHdr is 20 bytes
	if len(data) < msgHeaderMinSizeBytes {
		return msgHeader{}, ErrMalformedPacket
	}
	var header msgHeader

	header.EMsg = int32(binary.LittleEndian.Uint32(data[0:4]))
	header.TargetJobID = binary.LittleEndian.Uint64(data[4:12])
	header.SourceJobID = binary.LittleEndian.Uint64(data[12:20])

	// Then just read whatever is left into the body, if anything
	if len(data) > msgHeaderMinSizeBytes{
		header.Body = data[msgHeaderMinSizeBytes:]
	} else {
		header.Body = []byte{}
	}
	return header, nil
}

func (filter *HMACFilter) EncryptMessage(msg []byte) ([]byte, error) {
	nonce := make([]byte, 3)
	rand.Read(nonce)
	HMACInput := append(nonce, msg...)
	HMACFull := filter.HMACSHA1(HMACInput)
	initVector := append(HMACFull[:13], nonce...)

	encInitVector, err := filter.AESECBEncrypt(initVector)
	if err != nil {
		return nil, err
	}
	cipherText, err := filter.AESCBCEncrypt(msg, initVector)
	if err != nil {
		return nil, err
	}
	output := append(encInitVector, cipherText...)
	return output, nil
}

func (filter *HMACFilter) HMACSHA1(HMACInput []byte) []byte {
	h := hmac.New(sha1.New, filter.HMACSecret)
	h.Write(HMACInput)
	HMACResult := h.Sum(nil)
	return HMACResult
}

func (filter *HMACFilter) AESECBEncrypt(vector []byte) ([]byte, error) {
	// No padding needed because the vec is already 16 bytes
	block, err := aes.NewCipher(filter.AESKey)
	if err != nil {
		return nil, err
	}
	mode := ecb.NewECBEncrypter(block)
	encVector := make([]byte, len(vector))
	mode.CryptBlocks(encVector, vector)
	return encVector, nil
}

func (filter *HMACFilter) AESCBCEncrypt(msg, initVector []byte) ([]byte, error) {
	paddedMsg := pkcs7Pad(msg, aes.BlockSize)
	block, err := aes.NewCipher(filter.AESKey)
	if err != nil {
		return nil, err
	}
	encMsg := make([]byte, len(paddedMsg))
	mode := cipher.NewCBCEncrypter(block, initVector)
	mode.CryptBlocks(encMsg, paddedMsg)
	return encMsg, nil
}

func (filter *HMACFilter) DecryptMessage(encMsg []byte) ([]byte, error) {
	encInitVector := encMsg[:16]
	cipherText := encMsg[16:]
	initVector, err := filter.AESECBDecrypt(encInitVector)
	if err != nil {
		return nil, err
	}
	msg, err := filter.AESCBCDecrypt(cipherText, initVector)
	if err != nil {
		return nil, err
	}

	// Validate
	nonce := initVector[13:16]
	HMACInput := append(nonce, msg...)
	expected := filter.HMACSHA1(HMACInput)
	if !bytes.Equal(expected[0:13], initVector[0:13]) {
		return nil, ErrInvalidIVHash
	}
	return msg, nil
}

func (filter *HMACFilter) AESECBDecrypt(encVector []byte) ([]byte, error) {
	block, err := aes.NewCipher(filter.AESKey)
	if err != nil {
		return nil, err
	}
	mode := ecb.NewECBDecrypter(block)
	vector := make([]byte, len(encVector))
	mode.CryptBlocks(vector, encVector)
	return vector, nil
}

func (filter *HMACFilter) AESCBCDecrypt(cipherText, initVector []byte) ([]byte, error) {
	block, err := aes.NewCipher(filter.AESKey)
	if err != nil {
		return nil, err
	}
	msg := make([]byte, len(cipherText))
	mode := cipher.NewCBCDecrypter(block, initVector)
	mode.CryptBlocks(msg, cipherText)
	msg = pkcs7Unpad(msg)
	return msg, nil
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - (len(data) % blockSize)
	pad := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(data, pad...)
}

func pkcs7Unpad(data []byte) []byte {
	padding := int(data[len(data) - 1])
	return data[:len(data) - padding]
}

func getUniversePubKey(universe uint32) (any, error) {
	var universePublicKey = []byte{
		0x30, 0x81, 0x9D, 0x30, 0x0D, 0x06, 0x09, 0x2A, 0x86, 0x48, 0x86, 0xF7, 0x0D, 0x01, 0x01, 0x01,
        0x05, 0x00, 0x03, 0x81, 0x8B, 0x00, 0x30, 0x81, 0x87, 0x02, 0x81, 0x81, 0x00, 0xDF, 0xEC, 0x1A,
        0xD6, 0x2C, 0x10, 0x66, 0x2C, 0x17, 0x35, 0x3A, 0x14, 0xB0, 0x7C, 0x59, 0x11, 0x7F, 0x9D, 0xD3,
        0xD8, 0x2B, 0x7A, 0xE3, 0xE0, 0x15, 0xCD, 0x19, 0x1E, 0x46, 0xE8, 0x7B, 0x87, 0x74, 0xA2, 0x18,
        0x46, 0x31, 0xA9, 0x03, 0x14, 0x79, 0x82, 0x8E, 0xE9, 0x45, 0xA2, 0x49, 0x12, 0xA9, 0x23, 0x68,
        0x73, 0x89, 0xCF, 0x69, 0xA1, 0xB1, 0x61, 0x46, 0xBD, 0xC1, 0xBE, 0xBF, 0xD6, 0x01, 0x1B, 0xD8,
        0x81, 0xD4, 0xDC, 0x90, 0xFB, 0xFE, 0x4F, 0x52, 0x73, 0x66, 0xCB, 0x95, 0x70, 0xD7, 0xC5, 0x8E,
        0xBA, 0x1C, 0x7A, 0x33, 0x75, 0xA1, 0x62, 0x34, 0x46, 0xBB, 0x60, 0xB7, 0x80, 0x68, 0xFA, 0x13,
        0xA7, 0x7A, 0x8A, 0x37, 0x4B, 0x9E, 0xC6, 0xF4, 0x5D, 0x5F, 0x3A, 0x99, 0xF9, 0x9E, 0xC4, 0x3A,
        0xE9, 0x63, 0xA2, 0xBB, 0x88, 0x19, 0x28, 0xE0, 0xE7, 0x14, 0xC0, 0x42, 0x89, 0x02, 0x01, 0x11,
	}
	var universeBetaKey = []byte{
		0x30, 0x81, 0x9D, 0x30, 0x0D, 0x06, 0x09, 0x2A, 0x86, 0x48, 0x86, 0xF7, 0x0D, 0x01, 0x01, 0x01,
        0x05, 0x00, 0x03, 0x81, 0x8B, 0x00, 0x30, 0x81, 0x87, 0x02, 0x81, 0x81, 0x00, 0xAE, 0xD1, 0x4B,
        0xC0, 0xA3, 0x36, 0x8B, 0xA0, 0x39, 0x0B, 0x43, 0xDC, 0xED, 0x6A, 0xC8, 0xF2, 0xA3, 0xE4, 0x7E,
        0x09, 0x8C, 0x55, 0x2E, 0xE7, 0xE9, 0x3C, 0xBB, 0xE5, 0x5E, 0x0F, 0x18, 0x74, 0x54, 0x8F, 0xF3,
        0xBD, 0x56, 0x69, 0x5B, 0x13, 0x09, 0xAF, 0xC8, 0xBE, 0xB3, 0xA1, 0x48, 0x69, 0xE9, 0x83, 0x49,
        0x65, 0x8D, 0xD2, 0x93, 0x21, 0x2F, 0xB9, 0x1E, 0xFA, 0x74, 0x3B, 0x55, 0x22, 0x79, 0xBF, 0x85,
        0x18, 0xCB, 0x6D, 0x52, 0x44, 0x4E, 0x05, 0x92, 0x89, 0x6A, 0xA8, 0x99, 0xED, 0x44, 0xAE, 0xE2,
        0x66, 0x46, 0x42, 0x0C, 0xFB, 0x6E, 0x4C, 0x30, 0xC6, 0x6C, 0x5C, 0x16, 0xFF, 0xBA, 0x9C, 0xB9,
        0x78, 0x3F, 0x17, 0x4B, 0xCB, 0xC9, 0x01, 0x5D, 0x3E, 0x37, 0x70, 0xEC, 0x67, 0x5A, 0x33, 0x48,
        0xF7, 0x46, 0xCE, 0x58, 0xAA, 0xEC, 0xD9, 0xFF, 0x4A, 0x78, 0x6C, 0x83, 0x4B, 0x02, 0x01, 0x11,
	}
	var universeInternalKey = []byte{
		0x30, 0x81, 0x9D, 0x30, 0x0D, 0x06, 0x09, 0x2A, 0x86, 0x48, 0x86, 0xF7, 0x0D, 0x01, 0x01, 0x01,
        0x05, 0x00, 0x03, 0x81, 0x8B, 0x00, 0x30, 0x81, 0x87, 0x02, 0x81, 0x81, 0x00, 0xA8, 0xFE, 0x01,
        0x3B, 0xB6, 0xD7, 0x21, 0x4B, 0x53, 0x23, 0x6F, 0xA1, 0xAB, 0x4E, 0xF1, 0x07, 0x30, 0xA7, 0xC6,
        0x7E, 0x6A, 0x2C, 0xC2, 0x5D, 0x3A, 0xB8, 0x40, 0xCA, 0x59, 0x4D, 0x16, 0x2D, 0x74, 0xEB, 0x0E,
        0x72, 0x46, 0x29, 0xF9, 0xDE, 0x9B, 0xCE, 0x4B, 0x8C, 0xD0, 0xCA, 0xF4, 0x08, 0x94, 0x46, 0xA5,
        0x11, 0xAF, 0x3A, 0xCB, 0xB8, 0x4E, 0xDE, 0xC6, 0xD8, 0x85, 0x0A, 0x7D, 0xAA, 0x96, 0x0A, 0xEA,
        0x7B, 0x51, 0xD6, 0x22, 0x62, 0x5C, 0x1E, 0x58, 0xD7, 0x46, 0x1E, 0x09, 0xAE, 0x43, 0xA7, 0xC4,
        0x34, 0x69, 0xA2, 0xA5, 0xE8, 0x44, 0x76, 0x18, 0xE2, 0x3D, 0xB7, 0xC5, 0xA8, 0x96, 0xFD, 0xE5,
        0xB4, 0x4B, 0xF8, 0x40, 0x12, 0xA6, 0x17, 0x4E, 0xC4, 0xC1, 0x60, 0x0E, 0xB0, 0xC2, 0xB8, 0x40,
        0x4D, 0x9E, 0x76, 0x4C, 0x44, 0xF4, 0xFC, 0x6F, 0x14, 0x89, 0x73, 0xB4, 0x13, 0x02, 0x01, 0x11,
	}
	var universeDevKey = []byte{
		0x30, 0x81, 0x9D, 0x30, 0x0D, 0x06, 0x09, 0x2A, 0x86, 0x48, 0x86, 0xF7, 0x0D, 0x01, 0x01, 0x01,
        0x05, 0x00, 0x03, 0x81, 0x8B, 0x00, 0x30, 0x81, 0x87, 0x02, 0x81, 0x81, 0x00, 0xD0, 0x05, 0x2C,
        0xE9, 0x80, 0x95, 0xCD, 0x30, 0x83, 0xA8, 0xE9, 0x25, 0x96, 0x63, 0xCE, 0xCC, 0x48, 0x5D, 0x5C,
        0x52, 0x00, 0xDB, 0x1E, 0x78, 0xD7, 0x6A, 0x4C, 0x2C, 0xC8, 0x41, 0x8C, 0xCC, 0x87, 0x46, 0xFB,
        0x1B, 0xC9, 0xE8, 0x6E, 0x4F, 0x7A, 0x6B, 0xC3, 0xE7, 0x0F, 0xD5, 0xA9, 0x5D, 0x6C, 0xD4, 0xEE,
        0xA2, 0xCC, 0x80, 0x5A, 0xD3, 0xCE, 0x53, 0x59, 0xE6, 0x80, 0x91, 0xC4, 0xC0, 0xD5, 0xF0, 0x63,
        0x23, 0x91, 0x69, 0x70, 0xC5, 0xBB, 0xBD, 0x05, 0xE2, 0x4F, 0x7D, 0x90, 0x12, 0xED, 0xAC, 0x4F,
        0x86, 0x96, 0x3C, 0x89, 0xCC, 0x92, 0x15, 0x63, 0xCB, 0x57, 0x70, 0xB9, 0xC3, 0xAE, 0x08, 0x4F,
        0xC8, 0x56, 0x16, 0xB0, 0x0C, 0xC6, 0xC8, 0x8A, 0x80, 0xD2, 0x37, 0xF7, 0x7F, 0xAB, 0x93, 0xBB,
        0xE6, 0xDE, 0x95, 0x78, 0xB8, 0x11, 0xC9, 0xE5, 0x62, 0xAD, 0xBC, 0x0C, 0x87, 0x02, 0x01, 0x11,
	}

	var keyMap = map[uint32][]byte{
		1: universePublicKey,
		2: universeBetaKey,
		3: universeInternalKey,
		4: universeDevKey,
	}

	keyBytes, ok := keyMap[universe]
	if !ok {
		return nil, ErrInvalidUniverseKey
	}

	pubKey, err := x509.ParsePKIXPublicKey(keyBytes)
	if err != nil {
		return nil, err
	}

	return pubKey, nil
}
