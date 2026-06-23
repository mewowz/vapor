package vapor

import (
	"log/slog"
	"math/rand"
	"time"

	"github.com/mewowz/vapor/internal/steamproto"
	"google.golang.org/protobuf/proto"
)

var EOSTypeLinuxUnknown int32 = -203

type AnonymousAuthenticator struct {
	steamConn *SteamConnection
	connInfo  authConnectionInfo
	logger    *slog.Logger
}

type Authenticator struct {
	steamConn *SteamConnection
	connInfo  authConnectionInfo
	logger    *slog.Logger
}

type authConnectionInfo struct {
	ClientSessionID   int32
	SteamID           uint64
	CellID            uint32
	HeartbeatDuration time.Duration
}

type LogonCredentials struct {
	username      string
	password      string
	loginKey      string
	authCode      string
	twoFactorCode string
	loginID       uint32
}

func NewAnonymousAuthenticator(steamConn *SteamConnection) *AnonymousAuthenticator {
	return &AnonymousAuthenticator{
		steamConn: steamConn,
		logger:    steamConn.logger,
	}
}

func NewAuthenticator(steamConn *SteamConnection) *Authenticator {
	return &Authenticator{
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

func (auth *Authenticator) Logon(logonCredentials *LogonCredentials) error {
	logOnResponseListener, err := auth.steamConn.GetListenerForEMsg(EMsgClientLogOnResponse)
	if err != nil {
		return err
	}

	err = auth.submitClientLogonWithCredentials(logonCredentials)
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
		ProtocolVersion:      proto.Uint32(65580),
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

func (auth *Authenticator) handleClientLogOnResponse(message Message) error {
	logonResponseMsg := message.(*msgHeaderPB)
	logonResponseHeader := logonResponseMsg.header
	logonResponse := logonResponseMsg.Proto().(*steamproto.CMsgClientLogonResponse)
	if logonResponse.GetEresult() != 1 {
		auth.logger.Debug("got bad EResult", "EResult", logonResponse.GetEresult())
		return ErrBadEResult
	}
	auth.connInfo = authConnectionInfo{
		ClientSessionID:   logonResponseHeader.GetClientSessionid(),
		SteamID:           logonResponseHeader.GetSteamid(),
		CellID:            logonResponse.GetCellId(),
		HeartbeatDuration: time.Duration(logonResponse.GetHeartbeatSeconds()) * time.Second,
	}
	auth.steamConn.SetHeartbeatInterval(auth.connInfo.HeartbeatDuration)
	auth.steamConn.SetConnTimeout(auth.connInfo.HeartbeatDuration+(5*time.Second), false)

	return nil
}

func (auth *Authenticator) submitClientLogonWithCredentials(logonCredentials *LogonCredentials) error {
	if logonCredentials.username == "" {
		return ErrNeedUsername
	}

	clientLogon := &steamproto.CMsgClientLogon{
		ProtocolVersion:           proto.Uint32(65580),
		ClientPackageVersion:      proto.Uint32(1561159470),
		ShouldRememberPassword:    proto.Bool(true),
		SupportsRateLimitResponse: proto.Bool(true),
		ChatMode:                  proto.Uint32(2),
		AccountName:               proto.String(logonCredentials.username),
	}

	var loginID uint32
	if logonCredentials.loginID == 0 {
		loginID = rand.Uint32()
	} else {
		loginID = logonCredentials.loginID
	}
	clientLogon.ObfuscatedPrivateIp = &steamproto.CMsgIPAddress{
		Ip: &steamproto.CMsgIPAddress_V4{
			V4: loginID,
		},
	}

	if logonCredentials.loginKey != "" {
		clientLogon.LoginKey = proto.String(logonCredentials.loginKey)
	} else {
		clientLogon.Password = proto.String(logonCredentials.password)
	}

	eResultSentry, shaSentry, err := auth.getSentry(logonCredentials.username)
	if err != nil {
		return err
	}
	clientLogon.EresultSentryfile = proto.Int32(int32(eResultSentry))
	if eResultSentry == 1 {
		clientLogon.ShaSentryfile = shaSentry
	}

	if logonCredentials.authCode != "" {
		clientLogon.AuthCode = proto.String(logonCredentials.authCode)
	}

	if logonCredentials.twoFactorCode != "" {
		clientLogon.TwoFactorCode = proto.String(logonCredentials.twoFactorCode)
	}

	clientLogonHeader, err := NewMsgHeaderPB(EMsgClientLogon, clientLogon)
	if err != nil {
		return err
	}
	clientLogonHeader.header.Steamid = proto.Uint64(uint64(1)<<52 | uint64(1)<<56)

	auth.logger.Debug(
		"submitting client logon",
		"protocol_version", clientLogon.GetProtocolVersion(),
		"client_package_version", clientLogon.GetClientPackageVersion(),
		"account_name", clientLogon.GetAccountName(),
		"loginId", clientLogon.GetObfuscatedPrivateIp().GetV4(),
	)
	err = auth.steamConn.SubmitCMMsg(clientLogonHeader)
	return err
}

func (auth *Authenticator) getSentry(username string) (EResult, []byte, error) {
	// Stub
	return 9, nil, nil
}
