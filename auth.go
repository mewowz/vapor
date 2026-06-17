package vapor

import (
	"log/slog"
	"time"

	"github.com/mewowz/vapor/internal/steamproto"
	"google.golang.org/protobuf/proto"
)

var EOSTypeLinuxUnknown int32 = -203

type AnonymousAuthenticator struct {
	steamConn   *SteamConnection
	connInfo    authConnectionInfo
	licenseList []*steamproto.CMsgClientLicenseList_License
	logger      *slog.Logger
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
		logger:    steamConn.logger,
	}
}

func (auth *AnonymousAuthenticator) Logon() error {
	auth.logger.Info("anonymously logging into steam CM server")

	logOnResponseListener, err := auth.steamConn.GetListenerForEMsg(EMsgClientLogOnResponse)
	if err != nil {
		return err
	}

	// Submit the ClientLogon and wait for Steam to respond
	err = auth.submitClientLogon()
	if err != nil {
		return err
	}

	select {
	case message := <-logOnResponseListener.Read():
		err = auth.handleClientLogOnResponse(message)
		if err != nil {
			return err
		}
	case <-logOnResponseListener.Done():
		return ErrReturnChanCtxTimeout
	}

	return nil
}

func (auth *AnonymousAuthenticator) submitClientLogon() error {
	clientLogon := steamproto.CMsgClientLogon{
		ProtocolVersion:      proto.Uint32(66580),
		ClientPackageVersion: proto.Uint32(1561159470),
	}
	clientLogonHeader, err := NewMsgHeaderPB(EMsgClientLogon, &clientLogon)
	if err != nil {
		return err
	}
	clientLogonHeader.header.Steamid = proto.Uint64(uint64(10)<<52 | uint64(1)<<56)

	auth.logger.Debug(
		"submitting client logon",
		"protocol_version", clientLogon.ProtocolVersion,
		"client_package_version", clientLogon.ClientPackageVersion,
	)
	err = auth.steamConn.SubmitCMMsg(clientLogonHeader)
	return err
}

func (auth *AnonymousAuthenticator) handleClientLogOnResponse(message Message) error {
	// TODO: handle logonResponse.Eresult == 48 to try another
	// CM server. Needs to signal to the auth.steamConn to close the connection,
	// mark it as "bad," and try another CM server from the CM server list
	logonResponseMsg := message.(*msgHeaderPB)
	logonResponseHeader := logonResponseMsg.header
	logonResponse := logonResponseMsg.Proto().(*steamproto.CMsgClientLogonResponse)
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
	// TODO: find a better timeout to set after receiving the heartbeat interval
	auth.steamConn.SetConnTimeout(auth.connInfo.HeartbeatDuration+(5*time.Second), false)
	return nil
}
