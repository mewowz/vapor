package "vapor"

import "errors"

var (
	ErrBadMagic					= errors.New("bad magic packet")
	ErrMalformedPayload			= errors.New("malformed payload")
	ErrBadChannelEncryptRequest = errors.New("bad channel encrypt request")
)
