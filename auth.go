package vapor

import (
	"context"
	"errors"
	"time"

	"github.com/mewowz/vapor/internal/steamproto"
	"google.golang.org/protobuf/proto"
)

var EOSTypeLinuxUnknown int32 = -203

type AnonymousAuthenticator struct {
	steamConn   *SteamConnection
	connInfo    authConnectionInfo
	licenseList []*steamproto.CMsgClientLicenseList_License
}

type authConnectionInfo struct {
	ClientSessionID   int32
	SteamID           uint64
	CellID            uint32
	HeartbeatDuration time.Duration
}

func NewAnonymousAuthenticator(steamConn *SteamConnection) *AnonymousAuthenticator {
	return &AnonymousAuthenticator{
		steamConn: steamConn,
	}
}

func (auth *AnonymousAuthenticator) Logon() error {
	// This expects to receive a ClientLogOnResponse and then ClientLicenseList
	// is sent by the server automatically immediately afterwards
	returnChan, ctx, err := auth.submitClientLogon()
	if err != nil {
		return err
	}

	responseHeaderBytes, err := readReturnChan(returnChan, ctx)
	if err != nil {
		return err
	}

	responseHeader, err := NewMsgHeaderPBFromBytes(responseHeaderBytes)
	if err != nil {
		return err
	}
	if responseHeader.EMsg != EMsgMulti {
		return ErrBadEMsgResponse
	}
	cmsgMultiRaw, ok := responseHeader.Body.(*steamproto.CMsgMulti)
	if !ok {
		return ErrMalformedPayload
	}

	cmsgMultiBytes, err := UnpackCMsgMultiToBytes(*cmsgMultiRaw)
	if err != nil {
		return err
	}

	responses := make(map[EMsg]*msgHeaderPB)
	for _, msg := range cmsgMultiBytes {
		response, err := NewMsgHeaderPBFromBytes(msg)
		if err != nil {
			if errors.Is(err, ErrNoProtoForEMsg) {
				continue
			} else {
				return err
			}
		}
		_, alreadyExists := responses[response.EMsg]
		if alreadyExists {
			return ErrDuplicateEMsgInMulti
		} else {
			responses[response.EMsg] = response
		}
	}

	emsgHandlers := map[EMsg]func(msgHeaderPB) error{
		EMsgClientLogOnResponse: auth.handleClientLogOnResponse,
		// TODO: handle all known incoming messages in response
		// to a CMsgClientLogon
		// EMsgClientLicenseList:   auth.handleClientLicenseList,
	}
	for emsg, msgHeader := range responses {
		emsgHandler, ok := emsgHandlers[emsg]
		if !ok {
			continue
		} else {
			delete(emsgHandlers, emsg)
		}
		err := emsgHandler(*msgHeader)
		if err != nil {
			return err
		}
	}

	if len(emsgHandlers) != 0 {
		return ErrMissingMessageFromMulti
	}

	return nil
}

func (auth *AnonymousAuthenticator) submitClientLogon() (chan []byte, context.Context, error) {
	clientLogon := steamproto.CMsgClientLogon{
		ProtocolVersion:      proto.Uint32(66580),
		ClientPackageVersion: proto.Uint32(1561159470),
	}
	clientLogonHeader, err := NewMsgHeaderPB(EMsgClientLogon, &clientLogon)
	if err != nil {
		return nil, nil, err
	}
	clientLogonHeader.Header.Steamid = proto.Uint64(uint64(10)<<52 | uint64(1)<<56)

	clientLogonHeaderBytes, err := clientLogonHeader.Bytes()
	if err != nil {
		return nil, nil, err
	}
	returnChan, ctx, err := auth.steamConn.SubmitCMMsg(clientLogonHeaderBytes)
	if err != nil {
		return nil, nil, err
	}
	return returnChan, ctx, nil
}

func (auth *AnonymousAuthenticator) handleClientLicenseList(licenseListMsgHeader msgHeaderPB) error {
	// TODO: Move or delete this function. Currently unused due to the new changes and
	// realizing we don't actually receive a CMsgClientLicesnseList from steam after a CMsgClientLogon
	licenseList := licenseListMsgHeader.Body.(*steamproto.CMsgClientLicenseList)
	if licenseList.GetEresult() != 1 {
		return ErrBadEResult
	}
	auth.licenseList = licenseList.GetLicenses()
	return nil
}

func (auth *AnonymousAuthenticator) handleClientLogOnResponse(logonResponseMsgHeader msgHeaderPB) error {
	// TODO: handle logonResponse.Eresult == 48 to try another
	// CM server. Needs to signal to the auth.steamConn to close the connection,
	// mark it as "bad," and try another CM server from the CM server list
	logonResponseHeader := logonResponseMsgHeader.Header
	logonResponse := logonResponseMsgHeader.Body.(*steamproto.CMsgClientLogonResponse)
	if logonResponse.GetEresult() != 1 {
		return ErrBadEResult
	}
	auth.connInfo = authConnectionInfo{
		ClientSessionID:   logonResponseHeader.GetClientSessionid(),
		SteamID:           logonResponseHeader.GetSteamid(),
		CellID:            logonResponse.GetCellId(),
		HeartbeatDuration: time.Duration(logonResponse.GetHeartbeatSeconds()) * time.Second,
	}
	auth.steamConn.SetHeartbeatInterval(auth.connInfo.HeartbeatDuration)
	return nil
}

func readReturnChan(returnChan chan []byte, ctx context.Context) ([]byte, error) {
	// TODO: rename this function to something better - naming feels ambiguous
	select {
	case response := <-returnChan:
		return response, nil
	case <-ctx.Done():
		return nil, ErrReturnChanCtxTimeout
	}
}
