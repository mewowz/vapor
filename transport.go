package vapor

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/mewowz/vapor/internal/steamproto"
	"google.golang.org/protobuf/proto"
)

const (
	MagicPacket                  uint32 = 0x31305456 // VT01
	ChannelEncryptRequestMinSize uint   = 8
)

const DefaultJobID uint64 = math.MaxUint64

// CurrentProtocolVersion is following SteamKit's MsgClientLogon.CurrentProtocol.
const CurrentProtocolVersion uint32 = 65581

const msgHeaderMinSizeBytes = 20

const (
	DefaultDialTimeoutSeconds  = 10
	DefaultCMSubmissionTimeout = 10
)

type ConnectionState int

const (
	Disconnected ConnectionState = iota
	Connected
	Challenged
	Encrypted
)

type SteamConnection struct {
	connTimeout           time.Duration
	connState             ConnectionState
	connReader            *bufio.Reader
	conn                  net.Conn
	encFilter             *HMACFilter
	writeMut              sync.Mutex
	clientCMSubmits       chan ClientCMSubmission
	netLoopCtx            context.Context
	netLoopCancel         context.CancelCauseFunc
	netLoopMut            sync.RWMutex
	heartbeatIntervalChan chan time.Duration
}

type ClientCMSubmission struct {
	data       []byte
	ctx        context.Context
	ctxCancelF context.CancelFunc
	returnChan chan []byte
}

type serverListResponse struct {
	Response struct {
		ServerList []struct {
			Endpoint       string  `json:"endpoint"`
			LegacyEndpoint string  `json:"legacy_endpoint"`
			Type           string  `json:"type"`
			DC             string  `json:"dc"`
			Realm          string  `json:"realm"`
			Load           int     `json:"load"`
			WTDLoad        float64 `json:"wtd_load"`
		} `json:"serverlist"`
	} `json:"response"`
}

func NewSteamConnection(noResponseTimeout time.Duration) *SteamConnection {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(nil)
	return &SteamConnection{
		connTimeout:           noResponseTimeout,
		connState:             Disconnected,
		clientCMSubmits:       make(chan ClientCMSubmission, 32),
		netLoopCtx:            ctx,
		netLoopCancel:         cancel,
		heartbeatIntervalChan: make(chan time.Duration),
	}
}

func (c *SteamConnection) CMConnect(dialTimeout time.Duration) error {
	if c.connState != Disconnected {
		return ErrAlreadyConnectedToCM
	}
	serverHost, err := getCMServerHost(dialTimeout)
	if err != nil {
		return err
	}
	err = c.connectToCMServerTCP(serverHost, dialTimeout)
	if err != nil {
		return err
	}

	return nil
}

func (c *SteamConnection) StartNetLoop() error {
	if c.connState != Encrypted {
		return ErrConnNotEncrypted
	}

	c.netLoopMut.RLock()
	defer c.netLoopMut.RUnlock()

	select {
	case <-c.netLoopCtx.Done():
	default:
		return ErrNetLoopAlreadyRunning
	}

	go c.netLoop()
	return nil
}

func (c *SteamConnection) SubmitCMMsg(data []byte) (chan []byte, context.Context, error) {
	c.netLoopMut.RLock()
	defer c.netLoopMut.RUnlock()
	select {
	case <-c.netLoopCtx.Done():
		return nil, nil, ErrNetLoopNotRunning
	default:
	}

	returnChan := make(chan []byte, 1)
	ctx, ctxCancelF := context.WithTimeout(context.Background(), DefaultCMSubmissionTimeout*time.Second)
	submission := ClientCMSubmission{
		ctx:        ctx,
		ctxCancelF: ctxCancelF,
		returnChan: returnChan,
	}
	submission.data = make([]byte, len(data))
	copy(submission.data, data)

	c.clientCMSubmits <- submission
	return returnChan, ctx, nil
}

func (c *SteamConnection) SetHeartbeatInterval(interval time.Duration) error {
	c.netLoopMut.RLock()
	defer c.netLoopMut.RUnlock()
	select {
	case <-c.netLoopCtx.Done():
		return ErrNetLoopNotRunning
	default:
	}
	c.heartbeatIntervalChan <- interval
	return nil
}

func (c *SteamConnection) SetConnTimeout(timeout time.Duration, applyNow bool) {
	c.connTimeout = timeout
	if applyNow {
		c.resetConnDeadline()
	}
}

func (c *SteamConnection) netLoop() {
	c.netLoopMut.Lock()
	c.netLoopCtx, c.netLoopCancel = context.WithCancelCause(context.Background())
	c.netLoopMut.Unlock()

	var err error

	var heartbeatChan <-chan time.Time
	var t *time.Ticker

	cleanupFunc := func() {
		c.netLoopMut.Lock()
		defer c.netLoopMut.Unlock()
		c.netLoopCancel(err)
		if t != nil {
			t.Stop()
			t = nil
			heartbeatChan = nil
		}
		for {
			select {
			case client := <-c.clientCMSubmits:
				client.ctxCancelF()
			default:
				return
			}
		}
	}
	defer cleanupFunc()

	for {
		// There are heartbeats but I have not read the SteamKit source for heartbeats
		// nor implemented anything for it just yet
		select {
		case clientSubmission := <-c.clientCMSubmits:
			select {
			case <-clientSubmission.ctx.Done():
				// Skip if the client is timed-out or was cancelled
				continue
			default:
			}
			err = c.sendPayload(clientSubmission.data)
			// TODO: handle the various errors this could return
			if err != nil {
				return
			}

			data, err := c.getPayload()
			if err != nil {
				return
			}
			clientSubmission.returnChan <- data
		case interval := <-c.heartbeatIntervalChan:
			// Submitting an interval <= 0 acts as a sentinel value to ensure that the ticker
			// completely stops instead of constnatly ticking away or being re-used after being stopped
			if t != nil {
				t.Stop()
				t = nil
				heartbeatChan = nil
			}
			if interval > 0 {
				t = time.NewTicker(interval)
				heartbeatChan = t.C
			}
		case <-heartbeatChan:
			heartbeat := steamproto.CMsgClientHeartBeat{}
			heartbeatHeader, err := NewMsgHeaderPB(EMsgClientHeartBeat, &heartbeat)
			if err != nil {
				return
			}
			heartbeatHeaderBytes, err := heartbeatHeader.Bytes()
			if err != nil {
				return
			}
			err = c.sendPayload(heartbeatHeaderBytes)
			if err != nil {
				return
			}
		case <-c.netLoopCtx.Done():
			return
		}
	}
}

func (c *SteamConnection) resetConnDeadline() {
	c.conn.SetDeadline(time.Now().Add(c.connTimeout))
}

func (c *SteamConnection) connectToCMServerTCP(cmHost string, dialTimeout time.Duration) error {
	var err error
	network := "tcp"

	dialCtx, dialCancel := context.WithTimeout(context.Background(), dialTimeout)
	defer dialCancel()
	dialer := net.Dialer{}
	c.conn, err = dialer.DialContext(dialCtx, network, cmHost)
	if err != nil {
		return err
	}
	c.connState = Connected
	c.resetConnDeadline()

	c.connReader = bufio.NewReader(c.conn)

	err = c.establishEncryptedChannel()
	if err != nil {
		return err
	}

	c.connState = Encrypted

	err = c.sendClientHello()
	if err != nil {
		return err
	}

	return nil
}

func (c *SteamConnection) sendClientHello() error {
	clientHello := steamproto.CMsgClientHello{ProtocolVersion: proto.Uint32(CurrentProtocolVersion)}
	header, err := NewMsgHeaderPB(EMsgClientHello, &clientHello)
	if err != nil {
		return err
	}
	headerBytes, err := header.Bytes()
	if err != nil {
		return err
	}
	err = c.sendPayload(headerBytes)
	if err != nil {
		return err
	}
	return nil
}

func (c *SteamConnection) getRawPayload() ([]byte, error) {
	var err error
	defer func() {
		if err == nil {
			c.resetConnDeadline()
		}
	}()

	var header connectionHeader
	err = binary.Read(c.connReader, binary.LittleEndian, &header)
	if err != nil {
		return nil, err
	}

	if header.Magic != MagicPacket {
		return nil, ErrBadMagic
	}

	payload := make([]byte, header.PayloadLen)
	_, err = io.ReadFull(c.connReader, payload)
	if err != nil {
		return nil, err
	}

	return payload, nil
}

func (c *SteamConnection) getPayload() ([]byte, error) {
	rawPayload, err := c.getRawPayload()
	if err != nil {
		return nil, err
	}

	if c.connState == Encrypted {
		payload, err := c.encFilter.DecryptMessage(rawPayload)
		return payload, err
	} else {
		return rawPayload, nil
	}
}

func (c *SteamConnection) sendPayload(rawPayload []byte) error {
	if c.connState == Encrypted {
		payload, err := c.encFilter.EncryptMessage(rawPayload)
		if err != nil {
			return err
		}
		err = c.sendRawPayload(payload)
		return err
	} else {
		err := c.sendRawPayload(rawPayload)
		return err
	}
}

func (c *SteamConnection) sendRawPayload(payload []byte) error {
	var err error
	defer func() {
		if err == nil {
			c.resetConnDeadline()
		}
	}()
	header := newConnectionHeader(uint32(len(payload)))

	var data []byte
	data = binary.LittleEndian.AppendUint32(data, header.PayloadLen)
	data = binary.LittleEndian.AppendUint32(data, header.Magic)
	data = append(data, payload...)
	_, err = c.conn.Write(data)
	return err
}

// getCMServerHost will pull from Steam's API for CM servers
// @ https://api.steampowered.com/ISteamDirectory/GetCMListForConnect/v1
func getCMServerHost(timeout time.Duration) (string, error) {
	CMListURL := "https://api.steampowered.com/ISteamDirectory/GetCMListForConnect/v1"
	client := http.Client{Timeout: timeout}

	resp, err := client.Get(CMListURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", ErrBadCMServerFetch
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var result serverListResponse
	err = json.Unmarshal(body, &result)
	if err != nil {
		return "", err
	}

	for _, server := range result.Response.ServerList {
		if server.Type == "netfilter" {
			return server.Endpoint, nil
		}
	}

	return "", ErrNoCMServerFound
}
