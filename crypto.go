package vapor

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"encoding/binary"
	"hash/crc32"

	"github.com/andreburgaud/crypt2go/ecb"
)

type MessageFilter interface {
	Encrypt([]byte) ([]byte, error)
	Decrypt([]byte) ([]byte, error)
}

type HMACFilter struct {
	HMACSecret []byte
	AESKey     []byte
}

// emptyFilter acts as a mere pass-thru filter that doesn't do anything to the underlying
// data being passed through its methods Encrypt & Decrypt.
// Its use is to allow unencrypted transmission of data during the encryption
// handshake with the CM server
type emptyFilter struct{}

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
	channelEncryptResponseMsgHeader := msgHeader{EMsgChannelEncryptResponse, DefaultJobID, DefaultJobID, channelEncryptResponseBody}

	return tempSessionKey, channelEncryptResponseMsgHeader, nil
}

func NewHMACFilter(sessionKey []byte) *HMACFilter {
	return &HMACFilter{
		HMACSecret: sessionKey[:16],
		AESKey:     sessionKey[:32],
	}
}

func (filter *HMACFilter) Encrypt(msg []byte) ([]byte, error) {
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

func (filter *HMACFilter) Decrypt(encMsg []byte) ([]byte, error) {
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

func (f emptyFilter) Encrypt(data []byte) ([]byte, error) {
	return data, nil
}

func (f emptyFilter) Decrypt(data []byte) ([]byte, error) {
	return data, nil
}

func (c *SteamConnection) establishEncryptedChannel() error {
	payload, err := c.getPayload()
	if err != nil {
		return err
	}

	// The payload should just be the ChannelEncryptRequest
	header, err := parseMsgHeader(payload)
	if err != nil {
		return err
	}
	if header.Size() < ChannelEncryptRequestMinSize {
		return ErrBadChannelEncryptRequest
	}
	if header.EMsg == EMsgChannelEncryptRequest {
		c.connState = Challenged
	} else {
		return ErrBadChannelEncryptRequest
	}

	tempSessionKey, channelEncryptResponseMsgHeader, err := newChannelEncryptResponse(header)
	if err != nil {
		return err
	}

	// Send the channelEncryptResponseMsgHeader on the wire with proper serialization
	serializedEncryptResponse := make([]byte, channelEncryptResponseMsgHeader.Size())
	binary.LittleEndian.PutUint32(serializedEncryptResponse[:4], uint32(channelEncryptResponseMsgHeader.EMsg))
	binary.LittleEndian.PutUint64(serializedEncryptResponse[4:12], channelEncryptResponseMsgHeader.TargetJobID)
	binary.LittleEndian.PutUint64(serializedEncryptResponse[12:20], channelEncryptResponseMsgHeader.SourceJobID)
	copy(serializedEncryptResponse[20:], channelEncryptResponseMsgHeader.Body)
	err = c.sendPayload(serializedEncryptResponse)
	if err != nil {
		return err
	}

	payload, err = c.getPayload()
	if err != nil {
		return err
	}

	header, err = parseMsgHeader(payload)
	if err != nil {
		return err
	}
	if header.EMsg != EMsgChannelEncryptResult {
		return ErrBadChannelEncryptResult
	}

	EResult := binary.LittleEndian.Uint32(header.Body)
	if EResult != 1 {
		return ErrBadChannelEncryptResult
	}
	c.encFilter = NewHMACFilter(tempSessionKey)

	return nil
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - (len(data) % blockSize)
	pad := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(data, pad...)
}

func pkcs7Unpad(data []byte) []byte {
	padding := int(data[len(data)-1])
	return data[:len(data)-padding]
}
