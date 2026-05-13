package vapor

import "errors"

var (
	ErrBadMagic					= errors.New("bad magic packet")
	ErrMalformedPayload			= errors.New("malformed payload")
	ErrBadChannelEncryptRequest	= errors.New("bad channel encrypt request")
	ErrBadChannelEncryptResult	= errors.New("bad channel encrypt response")
	ErrInvalidUniverseKey		= errors.New("invalid universe key")
	ErrBadPublicKey				= errors.New("bad public key")
	ErrMalformedPacket			= errors.New("malformed packet")
)
