package vapor

import (
	"log/slog"
	"math/rand"
	"time"

	"github.com/mewowz/vapor/internal/steamproto"
	"google.golang.org/protobuf/proto"
)

type AuthenticationType int

const (
	Anonymous AuthenticationType = iota
	Credentialed
)

type authConnectionInfo struct {
	ClientSessionID   int32
	SteamID           uint64
	CellID            uint32
	HeartbeatDuration time.Duration
	AuthType          AuthenticationType
}

type LogonCredentials struct {
	username      string
	password      string
	loginKey      string
	authCode      string
	twoFactorCode string
	loginID       uint32
}

func LogonAnonymous(
	conn CMMessenger,
	logger *slog.Logger,
) (authConnectionInfo, error) {
	logOnResponseListener, err := conn.GetListenerForEMsg(EMsgClientLogOnResponse)
	if err != nil {
		return authConnectionInfo{}, err
	}
	err = submitAnonymousClientLogon(conn, logger)
	if err != nil {
		return authConnectionInfo{}, err
	}

	message, err := logOnResponseListener.Read()
	if err != nil {
		return authConnectionInfo{}, err
	}
	info, err := handleClientLogOnResponse(message, logger)
	info.AuthType = Anonymous
	return info, err
}

func LogonCredentialed(
	conn CMMessenger,
	logger *slog.Logger,
	credentials *LogonCredentials,
) (authConnectionInfo, error) {
	logOnResponseListener, err := conn.GetListenerForEMsg(EMsgClientLogOnResponse)
	if err != nil {
		return authConnectionInfo{}, err
	}

	err = submitCredentialedClientLogon(conn, logger, credentials)
	if err != nil {
		return authConnectionInfo{}, err
	}

	message, err := logOnResponseListener.Read()
	if err != nil {
		return authConnectionInfo{}, err
	}
	info, err := handleClientLogOnResponse(message, logger)
	info.AuthType = Credentialed
	return info, err
}

func submitCredentialedClientLogon(
	conn CMMessenger,
	logger *slog.Logger,
	credentials *LogonCredentials,
) error {
	if credentials.username == "" {
		return ErrNeedUsername
	}

	clientLogon := &steamproto.CMsgClientLogon{
		ProtocolVersion:           proto.Uint32(65580),
		ClientPackageVersion:      proto.Uint32(1561159470),
		ShouldRememberPassword:    proto.Bool(true),
		SupportsRateLimitResponse: proto.Bool(true),
		ChatMode:                  proto.Uint32(2),
		AccountName:               proto.String(credentials.username),
	}

	var loginID uint32
	if credentials.loginID == 0 {
		loginID = rand.Uint32()
	} else {
		loginID = credentials.loginID
	}
	clientLogon.ObfuscatedPrivateIp = &steamproto.CMsgIPAddress{
		Ip: &steamproto.CMsgIPAddress_V4{
			V4: loginID,
		},
	}

	if credentials.loginKey != "" {
		clientLogon.LoginKey = proto.String(credentials.loginKey)
	} else {
		clientLogon.Password = proto.String(credentials.password)
	}

	eResultSentry, shaSentry, err := getSentry(credentials.username)
	if err != nil {
		return err
	}
	clientLogon.EresultSentryfile = proto.Int32(int32(eResultSentry))
	if eResultSentry == 1 {
		clientLogon.ShaSentryfile = shaSentry
	}

	if credentials.authCode != "" {
		clientLogon.AuthCode = proto.String(credentials.authCode)
	}

	if credentials.twoFactorCode != "" {
		clientLogon.TwoFactorCode = proto.String(credentials.twoFactorCode)
	}

	clientLogonHeader, err := NewMsgHeaderPB(EMsgClientLogon, clientLogon)
	if err != nil {
		return err
	}
	clientLogonHeader.header.Steamid = proto.Uint64(uint64(1)<<52 | uint64(1)<<56)

	logger.Debug(
		"submitting client logon",
		"protocol_version", clientLogon.GetProtocolVersion(),
		"client_package_version", clientLogon.GetClientPackageVersion(),
		"account_name", clientLogon.GetAccountName(),
		"loginId", clientLogon.GetObfuscatedPrivateIp().GetV4(),
	)
	err = conn.SubmitCMMsg(clientLogonHeader)
	return err
}

func submitAnonymousClientLogon(
	conn CMMessenger,
	logger *slog.Logger,
) error {
	clientLogon := steamproto.CMsgClientLogon{
		ProtocolVersion:      proto.Uint32(65580),
		ClientPackageVersion: proto.Uint32(1561159470),
	}
	clientLogonHeader, err := NewMsgHeaderPB(EMsgClientLogon, &clientLogon)
	if err != nil {
		return err
	}
	clientLogonHeader.header.Steamid = proto.Uint64(uint64(10)<<52 | uint64(1)<<56)

	logger.Debug(
		"submitting client logon",
		"protocol_version", clientLogon.ProtocolVersion,
		"client_package_version", clientLogon.ClientPackageVersion,
	)
	err = conn.SubmitCMMsg(clientLogonHeader)
	return err
}

func handleClientLogOnResponse(
	message Message,
	logger *slog.Logger,
) (authConnectionInfo, error) {
	logonResponseMsg := message.(*msgHeaderPB)
	logonResponseHeader := logonResponseMsg.header
	logonResponse := logonResponseMsg.Proto().(*steamproto.CMsgClientLogonResponse)
	if logonResponse.GetEresult() != 1 {
		logger.Debug("got bad EResult", "EResult", logonResponse.GetEresult())
		return authConnectionInfo{}, ErrBadEResult
	}
	return authConnectionInfo{
		ClientSessionID:   logonResponseHeader.GetClientSessionid(),
		SteamID:           logonResponseHeader.GetSteamid(),
		CellID:            logonResponse.GetCellId(),
		HeartbeatDuration: time.Duration(logonResponse.GetHeartbeatSeconds()) * time.Second,
	}, nil
}

func getSentry(username string) (EResult, []byte, error) {
	// Stub - copies what steam-python does if a file isn't found
	// EResultFileNotFound = 9
	return 9, nil, nil
}
