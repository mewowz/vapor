package vapor

import (
	"context"
	"time"

	"github.com/mewowz/vapor/internal/steamproto"
	"google.golang.org/protobuf/proto"
)

var EOSTypeLinuxUnknown int32 = -203

type AnonymousAuthenticator struct {
	steamConn *SteamConnection
	connInfo authConnectionInfo
	licenseList []*steamproto.CMsgClientLicenseList_License
}

type authConnectionInfo struct {
	ClientSessionID int32
	SteamID uint64
	CellID uint32
	HeartbeatSeconds time.Duration
}

func NewAnonymousAuthenticator(steamConn *SteamConnection) (*AnonymousAuthenticator ) {
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

	logonResponseMsgHeader, err := parseResponseHeaderBytes(responseHeaderBytes, EMsgClientLogonResponse)
	if err != nil {
		return err
	}
	err = auth.handleLogonResponse(*logonResponseMsgHeader)
	if err != nil {
		return err
	}

	responseHeaderBytes, err = readReturnChan(returnChan, ctx)
	if err != nil {
		return err
	}

	licenseListMsgHeader, err := parseResponseHeaderBytes(responseHeaderBytes, EMsgClientLicenseList)
	if err != nil {
		return err
	}
	err = auth.handleLicenseList(*licenseListMsgHeader)
	if err != nil {
		return err
	}
	return nil
}

func (auth *AnonymousAuthenticator) submitClientLogon() (chan []byte, context.Context, error) {
	expectedReturnPacketsCount := 2
	clientLogon := steamproto.CMsgClientLogon{
		ProtocolVersion: proto.Uint32(CurrentProtocolVersion),
		ClientOsType: proto.Uint32(uint32(EOSTypeLinuxUnknown)),
		ClientLanguage: proto.String("english"),
		CellId: proto.Uint32(0),
	}
	clientLogonHeader, err := NewMsgHeaderPB(EMsgClientLogon, &clientLogon)
	if err != nil {
		return nil, nil, err
	}

	clientLogonHeaderBytes, err := clientLogonHeader.Bytes()
	if err != nil {
		return nil, nil, err
	}
	returnChan, ctx, err := auth.steamConn.SubmitCMMsg(clientLogonHeaderBytes, expectedReturnPacketsCount)
	if err != nil {
		return nil, nil, err
	}
	return returnChan, ctx, nil
}


func (auth *AnonymousAuthenticator) handleLicenseList(licenseListMsgHeader msgHeaderPB) error {
	licenseList := licenseListMsgHeader.Body.(*steamproto.CMsgClientLicenseList)
	if licenseList.GetEresult() != 1 {
		return ErrBadEResult
	}
	auth.licenseList = licenseList.GetLicenses()
	return nil
}

func (auth *AnonymousAuthenticator) handleLogonResponse(logonResponseMsgHeader msgHeaderPB) error {
	// TODO: handle logonResponse.Eresult == 48 to try another
	// CM server. Needs to signal to the auth.steamConn to close the connection,
	// mark it as "bad," and try another CM server from the CM server list
	logonResponseHeader := logonResponseMsgHeader.Header
	logonResponse := logonResponseMsgHeader.Body.(*steamproto.CMsgClientLogonResponse)
	if logonResponse.GetEresult() != 1 {
		return ErrBadEResult
	}
	auth.connInfo = authConnectionInfo{
		ClientSessionID: logonResponseHeader.GetClientSessionid(),
		SteamID: logonResponseHeader.GetSteamid(),
		CellID: logonResponse.GetCellId(),
		HeartbeatSeconds: time.Duration(logonResponse.GetHeartbeatSeconds()) * time.Second,
	}
	// TODO: After proper implementation of SteamConnection.StartHeartbeatTicker
	//auth.steamConn.StartHeartbeatTicker(auth.connInfo.HeartbeatSeconds)
	return nil
}

func readReturnChan(returnChan chan []byte, ctx context.Context) ([]byte, error) {
	// Not a fan of the naming for this function, so I'll rename it later
	// TODO: rename this function to something better - naming feels ambiguous
	select {
	case response := <-returnChan:
		return response, nil
	case <-ctx.Done():
		return nil, ErrReturnChanCtxTimeout
	}
}

func parseResponseHeaderBytes(responseHeaderBytes []byte, expectedEMsg int32) (*msgHeaderPB, error) {
	responseHeader, err := NewMsgHeaderPBFromBytes(responseHeaderBytes)
	if err != nil {
		return nil, err
	} else if responseHeader.EMsg != expectedEMsg { 
		return nil, ErrBadEMsgResponse
	}
	return responseHeader, nil

}
