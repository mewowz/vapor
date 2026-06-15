package vapor

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"math/rand"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
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
	encFilter             MessageFilter
	writeMut              sync.Mutex
	clientCMSubmits       chan ClientCMSubmission
	netLoopCtx            context.Context
	netLoopCancel         context.CancelCauseFunc
	netLoopMut            sync.RWMutex
	heartbeatIntervalChan chan time.Duration
	dispatcher            *Dispatcher
	currentJobID          *atomic.Uint64
	logger                *slog.Logger
}

type ClientCMSubmission struct {
	data       []byte
	ctx        context.Context
	ctxCancelF context.CancelFunc
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

type payloadResult struct {
	data []byte
	err  error
}

func NewSteamConnection(noResponseTimeout time.Duration, logger *slog.Logger) *SteamConnection {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(nil)
	if logger == nil {
		logger = slog.Default()
	}
	return &SteamConnection{
		connTimeout:           noResponseTimeout,
		connState:             Disconnected,
		clientCMSubmits:       make(chan ClientCMSubmission, 32),
		encFilter:             emptyFilter{},
		netLoopCtx:            ctx,
		netLoopCancel:         cancel,
		heartbeatIntervalChan: make(chan time.Duration),
		logger:                logger,
		currentJobID:          &atomic.Uint64{},
		dispatcher:            NewDispatcher(logger),
	}
}

func (c *SteamConnection) CMConnect(dialTimeout time.Duration) error {
	if c.connState != Disconnected {
		return ErrAlreadyConnectedToCM
	}
	c.logger.Info("fetching steam server host")
	serverHost, availableHosts, err := getCMServerHost(dialTimeout)
	if err != nil {
		return err
	}
	c.logger.Debug("fetch CM server host list", "chosen_host", serverHost, "hosts_available", availableHosts)
	c.logger.Info("connecting to CM server", "host", serverHost, "protocol", "tcp")
	err = c.connectToCMServerTCP(serverHost, dialTimeout)
	if err != nil {
		return err
	}
	c.logger.Info("connected to CM server", "host", serverHost, "protocol", "tcp")

	return nil
}

func (c *SteamConnection) StartNetLoop() error {
	if c.connState != Encrypted {
		return ErrConnNotEncrypted
	}

	c.netLoopMut.Lock()
	defer c.netLoopMut.Unlock()

	select {
	case <-c.netLoopCtx.Done():
	default:
		return ErrNetLoopAlreadyRunning
	}

	c.logger.Info("initializing netloop")
	c.netLoopCtx, c.netLoopCancel = context.WithCancelCause(context.Background())

	go c.netLoopRead()
	go c.netLoopWrite()
	c.logger.Info("netloop started")
	return nil
}

func (c *SteamConnection) StopNetLoop(cancelErr error) error {
	c.netLoopMut.Lock()
	defer c.netLoopMut.Unlock()

	select {
	case <-c.netLoopCtx.Done():
		return ErrNetLoopNotRunning
	default:
	}

	c.netLoopCancel(cancelErr)

	return nil
}

func (c *SteamConnection) SubmitCMMsg(message Message) (chan Message, context.Context, error) {
	c.netLoopMut.RLock()
	defer c.netLoopMut.RUnlock()
	select {
	case <-c.netLoopCtx.Done():
		return nil, nil, ErrNetLoopNotRunning
	default:
	}

	returnChan := make(chan Message, 1)
	jobID := c.currentJobID.Add(1)
	err := c.dispatcher.Register(jobID, returnChan)
	if err != nil {
		c.logger.Debug("error registering jobID to dispatcher", "jobID", jobID, "err", err)
		return nil, nil, err
	}
	message.SetSourceJobID(jobID)

	ctx, ctxCancelF := context.WithTimeout(context.Background(), DefaultCMSubmissionTimeout*time.Second)
	submission := ClientCMSubmission{
		ctx:        ctx,
		ctxCancelF: ctxCancelF,
	}

	msgData, err := message.Bytes()
	if err != nil {
		c.logger.Debug("error decoding message to bytes", "err", err)
		return nil, nil, err
	}
	submission.data = make([]byte, len(msgData))
	copy(submission.data, msgData)

	c.clientCMSubmits <- submission
	c.logger.Debug("queued CM message", "data_len", len(msgData))
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
	c.logger.Debug("set heartbeat interval", "duration", interval)
	return nil
}

func (c *SteamConnection) SetConnTimeout(timeout time.Duration, applyNow bool) {
	c.connTimeout = timeout
	if applyNow {
		c.resetConnDeadline()
	}
	c.logger.Debug("set connection timeout", "timeout", timeout)
}

func (c *SteamConnection) getPayloadAsync(returnChan chan<- payloadResult) {
	// This will block until a full payload is read
	data, err := c.getPayload()
	returnChan <- payloadResult{data: data, err: err}
}

func (c *SteamConnection) netLoopRead() {
	// exitErr is the error to be set if and only if the netloop must quit
	// due to an irrecoverable error, otherwise nil
	var exitErr error

	// Stub for now
	cleanupF := func() {
		c.logger.Debug("deconstructing netLoopRead", "err", exitErr)
	}
	defer cleanupF()

	payloadAsyncChan := make(chan payloadResult, 1)
	for {
		go c.getPayloadAsync(payloadAsyncChan)

		select {
		case payload := <-payloadAsyncChan:
			if payload.err != nil {
				c.logger.Debug("got error while reading payload", "err", payload.err)
				// TODO: better error handling for cases such as timeouts
				exitErr = payload.err
				return
			}
			err := c.dispatcher.DispatchMessage(payload.data)
			if err != nil {
				c.logger.Debug("got error while dispatching message; continuing", "err", err)
			}
		case <-c.netLoopCtx.Done():
			return
		}
	}
}

func (c *SteamConnection) netLoopWrite() {
	var exitErr error
	var heartbeatChan <-chan time.Time
	var t *time.Ticker

	cleanupF := func() {
		c.logger.Debug("deconstucting netLoopWrite", "err", exitErr)
		if t != nil {
			t.Stop()
			t = nil
			heartbeatChan = nil
		}
		c.logger.Debug("deconstructed heartbeat ticker and chan")
		i := 0
		for {
			select {
			case client := <-c.clientCMSubmits:
				client.ctxCancelF()
				i += 1
			default:
				c.logger.Debug("canceled remaining CM queue", "cancel_count", i)
				return
			}
		}
	}
	defer cleanupF()

	for {
		select {
		case clientSubmission := <-c.clientCMSubmits:
			c.logger.Debug("handling outgoing CM message")

			select {
			case <-clientSubmission.ctx.Done():
				// Skip if the client is timed-out or was canceled
				c.logger.Debug("skipping client message submission", "reason", "context is done")
				continue
			default:
			}

			c.logger.Debug("sending outgoing CM message")
			err := c.sendPayload(clientSubmission.data)
			// TODO: handle the various errors this could return
			if err != nil {
				exitErr = err
				return
			}
			c.logger.Debug("done sending outgoing CM message")
		case interval := <-c.heartbeatIntervalChan:
			// Submitting an interval <= 0 acts as a sentinel value to ensure that the ticker
			// completely stops instead of constnatly ticking away or being re-used after being stopped
			c.logger.Debug("updating heartbeat interval", "interval", interval)
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
			c.logger.Debug("sending heartbeat")
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
			c.logger.Info("stopping netloop")
			return
		}
	}
}

func (c *SteamConnection) resetConnDeadline() {
	deadline := time.Now().Add(c.connTimeout)
	c.logger.Debug("resetting connection deadline", "new_deadline", deadline)
	c.conn.SetDeadline(deadline)
}

func (c *SteamConnection) connectToCMServerTCP(cmHost string, dialTimeout time.Duration) error {
	var err error
	network := "tcp"

	c.logger.Debug("dialing CM server", "host", cmHost, "dial_timeout", dialTimeout)
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

	c.logger.Debug("establishing encrypted channel", "host", cmHost)
	err = c.establishEncryptedChannel()
	if err != nil {
		return err
	}
	c.logger.Debug("established encrypted channel", "host", cmHost)

	c.connState = Encrypted

	c.logger.Info("sending ClientHello")
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

func (c *SteamConnection) getPayload() ([]byte, error) {
	var err error
	defer func() {
		if err == nil {
			c.resetConnDeadline()
		}
	}()

	var header connectionHeader
	err = binary.Read(c.connReader, binary.LittleEndian, &header)
	if err != nil {
		c.logger.Debug("failed to read payload", "err", err)
		return nil, err
	}

	if header.Magic != MagicPacket {
		return nil, ErrBadMagic
	}

	rawPayload := make([]byte, header.PayloadLen)
	_, err = io.ReadFull(c.connReader, rawPayload)
	if err != nil {
		return nil, err
	}
	payload, err := c.encFilter.Decrypt(rawPayload)
	return payload, err
}

func (c *SteamConnection) sendPayload(rawPayload []byte) error {
	var err error
	defer func() {
		if err == nil {
			c.resetConnDeadline()
		}
	}()

	payload, err := c.encFilter.Encrypt(rawPayload)
	if err != nil {
		c.logger.Debug("failed to send payload", "err", err)
		return err
	}

	var data []byte
	header := newConnectionHeader(uint32(len(payload)))
	data = binary.LittleEndian.AppendUint32(data, header.PayloadLen)
	data = binary.LittleEndian.AppendUint32(data, header.Magic)
	data = append(data, payload...)
	_, err = c.conn.Write(data)
	return err
}

// getCMServerHost will pull from Steam's API for CM servers
// @ https://api.steampowered.com/ISteamDirectory/GetCMListForConnect/v1
func getCMServerHost(timeout time.Duration) (string, []string, error) {
	CMListURL := "https://api.steampowered.com/ISteamDirectory/GetCMListForConnect/v1"
	client := http.Client{Timeout: timeout}

	resp, err := client.Get(CMListURL)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", nil, ErrBadCMServerFetch
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, err
	}
	var result serverListResponse
	err = json.Unmarshal(body, &result)
	if err != nil {
		return "", nil, err
	}

	serverList := []string{}
	for _, server := range result.Response.ServerList {
		if server.Type == "netfilter" {
			//return server.Endpoint, nil
			serverList = append(serverList, server.Endpoint)
		}
	}

	if len(serverList) == 0 {
		return "", nil, ErrNoCMServerFound
	} else {
		return serverList[rand.Intn(len(serverList))], serverList, nil
	}
}
