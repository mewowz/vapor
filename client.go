package vapor

import (
	"fmt"
	"log/slog"
	"time"
)

type SteamClient struct {
	connection        *SteamConnection
	connectionTimeout time.Duration
	apps              map[string]AppEntry
	packages          map[string]PackageEntry
	logger            *slog.Logger
}

type SteamClientOption func(*SteamClient) error

func WithLogger(l *slog.Logger) SteamClientOption {
	return func(c *SteamClient) error {
		c.logger = l
		return nil
	}
}

func WithTimeout(t time.Duration) SteamClientOption {
	return func(c *SteamClient) error {
		if t < 0 {
			return fmt.Errorf("expected non-negative timeout, got %v", t)
		}
		c.connectionTimeout = t
		return nil
	}
}

func NewSteamClient(opts ...SteamClientOption) (*SteamClient, error) {
	client := &SteamClient{
		logger:            slog.New(slog.DiscardHandler),
		connectionTimeout: DefaultDialTimeoutSeconds,
	}
	for _, opt := range opts {
		err := opt(client)
		if err != nil {
			return nil, err
		}
	}
	client.connection = NewSteamConnection(
		client.connectionTimeout,
		client.logger,
	)

	return client, nil
}

func (c *SteamClient) Connect() error {
	return c.connection.CMConnect(c.connectionTimeout)
}

func (c *SteamClient) Logon(creds LogonCredentials) error {
	return c.logon(&creds)
}

func (c *SteamClient) LogonAnonymous() error {
	return c.logon(nil)
}

func (c *SteamClient) GetConnectionState() ConnectionState {
	return c.connection.connState
}

func (c *SteamClient) logon(creds *LogonCredentials) error {
	if c.connection.connState != Encrypted {
		return ErrConnNotEncrypted
	}
	var err error
	var authInfo authConnectionInfo
	if creds == nil {
		authInfo, err = LogonAnonymous(
			c.connection,
			c.logger,
		)
	} else {
		authInfo, err = LogonCredentialed(
			c.connection,
			c.logger,
			creds,
		)
	}
	if err != nil {
		return err
	}
	err = c.connection.setConnectionInfo(&authInfo)
	return err
}
