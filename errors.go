package vapor

import "errors"

var (
	ErrBadMagic                 = errors.New("bad magic packet")
	ErrMalformedPayload         = errors.New("malformed payload")
	ErrBadChannelEncryptRequest = errors.New("bad channel encrypt request")
	ErrBadChannelEncryptResult  = errors.New("bad channel encrypt response")
	ErrInvalidUniverseKey       = errors.New("invalid universe key")
	ErrBadPublicKey             = errors.New("bad public key")
	ErrMalformedPacket          = errors.New("malformed packet")
	ErrInvalidIVHash            = errors.New("invalid init vector SHA1 hash")
	ErrAlreadyConnectedToCM     = errors.New("already connected to CM server")
	ErrBadCMServerFetch         = errors.New("bad status code fetching CM server list")
	ErrNoCMServerFound          = errors.New("no CM server could be found")
	ErrNoProtoForEMsg           = errors.New("no matching protobuf type for given EMsg")
	ErrConnNotEncrypted         = errors.New("connection is not encrypted")
	ErrNetLoopNotRunning		= errors.New("netloop is not running")
)
