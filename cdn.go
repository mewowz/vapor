package vapor

import (
	"log/slog"

	steamproto "github.com/mewowz/vapor/internal/steamproto"
	"google.golang.org/protobuf/proto"
)

func GetDepotDecryptionKey(
	depotID,
	appID uint32,
	conn CMMessenger,
	logger *slog.Logger,
) ([]byte, error) {
	decryptionKeyListener, err := conn.GetListenerForEMsg(EMsgClientGetDepotDecryptionKeyResponse)
	if err != nil {
		return nil, err
	}

	err = submitDecryptionKeyRequest(
		depotID,
		appID,
		conn,
		logger,
	)
	if err != nil {
		return nil, err
	}

	decryptionKeyResponse, err := decryptionKeyListener.Read()
	if err != nil {
		return nil, err
	}

	decryptionKey, err := handleDecryptionKeyResponse(
		decryptionKeyResponse,
		logger,
	)

	return decryptionKey, err
}

func submitDecryptionKeyRequest(
	depotID,
	appID uint32,
	conn CMMessenger,
	logger *slog.Logger,
) error {
	decryptionKeyRequest := &steamproto.CMsgClientGetDepotDecryptionKey{
		DepotId: proto.Uint32(depotID),
		AppId:   proto.Uint32(appID),
	}

	decryptionKeyRequestHeader, err := NewMsgHeaderPB(
		EMsgClientGetDepotDecryptionKey,
		decryptionKeyRequest,
	)
	if err != nil {
		return err
	}

	err = conn.SubmitCMMsg(decryptionKeyRequestHeader)
	return err
}

func handleDecryptionKeyResponse(
	message Message,
	logger *slog.Logger,
) ([]byte, error) {
	decryptionKeyResponseProto := message.Proto().(*steamproto.CMsgClientGetDepotDecryptionKeyResponse)
	if decryptionKeyResponseProto.GetEresult() != 1 {
		logger.Debug("bad EResult", "EResult", decryptionKeyResponseProto.GetEresult())
		return nil, ErrBadEResult
	}

	return decryptionKeyResponseProto.GetDepotEncryptionKey(), nil
}
