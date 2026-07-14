package vapor

import (
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mewowz/vapor/internal/steamproto"
)

type EMsgListener struct {
	emsg        EMsg
	returnChan  chan Message
	ctx         context.Context
	ctxCancelF  context.CancelFunc
	retainInMap atomic.Bool
	mut         sync.Mutex
	inUse       bool
}

type Dispatcher struct {
	emsgChanMap map[EMsg]*EMsgListener
	listeners   []*EMsgListener
	logger      *slog.Logger
}

func NewDispatcher(logger *slog.Logger) *Dispatcher {
	return &Dispatcher{
		emsgChanMap: make(map[EMsg]*EMsgListener),
		logger:      logger,
	}
}

func NewEMsgListener(emsg EMsg) *EMsgListener {
	ctx, ctxCancelF := context.WithTimeout(context.Background(), DefaultCMSubmissionTimeout*time.Second)
	listener := &EMsgListener{
		emsg:       emsg,
		returnChan: make(chan Message, 1),
		ctx:        ctx,
		ctxCancelF: ctxCancelF,
	}
	return listener
}

func (l *EMsgListener) Read() (Message, error) {
	l.mut.Lock()
	if l.inUse {
		l.mut.Unlock()
		return nil, ErrListenerConcurrentRead
	}

	l.inUse = true
	ctx := l.ctx
	l.mut.Unlock()

	var message Message
	var err error
	select {
	case message = <-l.returnChan:
	case <-ctx.Done():
		err = ErrReturnChanCtxTimeout
	}

	l.mut.Lock()
	if err == nil {
		l.ctx, l.ctxCancelF = context.WithTimeout(
			context.Background(), DefaultCMSubmissionTimeout*time.Second,
		)
	}
	l.inUse = false
	l.mut.Unlock()

	return message, err
}

func (l *EMsgListener) Done() <-chan struct{} {
	l.mut.Lock()
	done := l.ctx.Done()
	l.mut.Unlock()
	return done
}

func (d *Dispatcher) DispatchMessage(msgBytes []byte) error {
	d.cleanupDispatchMap()

	rawEmsg := EMsg(binary.LittleEndian.Uint32(msgBytes[:4]))
	isProto := rawEmsg&0x80000000 != 0
	emsg := rawEmsg & 0x7FFFFFFF

	if isProto {
		message, err := NewMsgHeaderPBFromBytes(msgBytes)
		if err != nil {
			d.logger.Debug("got error while deserializing ProtoBuf message from bytes", "err", err)
			return err
		}

		if emsg == EMsgMulti {
			err = d.dispatchMulti(message)
			return err
		}
		d.logger.Debug("got Message to dispatch", "EMsg", emsg)
		err = d.writeMessageToCaller(message)
		return err
	}

	// These EMsg's are specifically the messages ONLY used in the encryption
	// handshake at the very beginning of the connection.
	// They should not end up here as DispatchMessage() is to be called only by the
	// netLoopRead, which only runs after the encryption handshake is complete.
	encHandshakeEMsgs := []EMsg{1303, 1304, 1305}
	if slices.Contains(encHandshakeEMsgs, emsg) {
		d.logger.Error("encryption handshake EMsg in dispatcher", "EMsg", emsg)
		return nil
	} else {
		message := NewExtendedMsgHeaderFromBytes(msgBytes)
		//err := d.dispatchExtendedMsg(message)
		err := d.writeMessageToCaller(message)
		return err
	}
}

func (d *Dispatcher) dispatchMulti(message *msgHeaderPB) error {
	d.logger.Debug("dispatching EMsgMulti")
	// For now, I want this to pacnic if the body isn't a CMsgMulti because
	// that'll mean Steam, for some reason, sent EMsg = 1 without a correct body.
	// Then, I'll implement some handler, but I'll defer that for now
	protoBody := message.Proto().(*steamproto.CMsgMulti)
	unpackedMulti, err := UnpackCMsgMultiToBytes(protoBody)
	if err != nil {
		return err
	}
	for _, unpackedMessageBytes := range unpackedMulti {
		err = d.DispatchMessage(unpackedMessageBytes)
		if errors.Is(err, ErrNoProtoForEMsg) {
			rawEmsg := EMsg(binary.LittleEndian.Uint32(unpackedMessageBytes[:4]))
			emsg := rawEmsg & 0x7FFFFFFF
			d.logger.Debug("no proto handler for EMsg", "EMsg", emsg)
			continue
		} else if errors.Is(err, ErrNoChanForJob) {
			continue
		} else {
			return err
		}
	}
	return nil
}

func (d *Dispatcher) Register(emsg EMsg) (*EMsgListener, error) {
	d.cleanupDispatchMap()
	_, exists := d.emsgChanMap[emsg]
	if exists {
		return nil, ErrJobIDAlreadyInDispatcher
	}
	listener := NewEMsgListener(emsg)
	d.emsgChanMap[emsg] = listener
	d.listeners = append(d.listeners, listener)
	return listener, nil
}

func (d *Dispatcher) writeMessageToCaller(message Message) error {
	listener, exists := d.emsgChanMap[message.EMsg()]
	if !exists {
		d.logger.Debug("no mapping for message", "EMsg", message.EMsg())
		return ErrNoChanForJob
	}
	select {
	case listener.returnChan <- message:
	default:
		d.logger.Error("blocking on returnChan", "EMsg", message.EMsg())
		panic("returnChan is never allowed to block")
	}
	//delete(d.emsgChanMap, message.EMsg())

	if !listener.retainInMap.Load() {
		d.removeListener(listener)
	}
	return nil
}

func (d *Dispatcher) removeListener(listener *EMsgListener) {
	for i := range len(d.listeners) {
		if d.listeners[i].emsg == listener.emsg {
			d.listeners[i] = d.listeners[len(d.listeners)-1]
			d.listeners = d.listeners[:len(d.listeners)-1]
			delete(d.emsgChanMap, listener.emsg)
			break
		}
	}
}

func (d *Dispatcher) cleanupDispatchMap() {
	listenerListCopy := make([]*EMsgListener, len(d.listeners))
	copy(listenerListCopy, d.listeners)
	for _, listener := range listenerListCopy {
		if listener.isDeadAndFree() {
			d.removeListener(listener)
		}
	}
}

func (d *Dispatcher) cancelAllListeners() {
	for _, listener := range d.listeners {
		listener.cancel()
	}
	d.listeners = []*EMsgListener{}
	d.emsgChanMap = make(map[EMsg]*EMsgListener)
}

func (l *EMsgListener) cancel() {
	l.mut.Lock()
	cancel := l.ctxCancelF
	l.mut.Unlock()
	cancel()
}

func (l *EMsgListener) isDeadAndFree() bool {
	l.mut.Lock()
	defer l.mut.Unlock()
	select {
	case <-l.ctx.Done():
		return !l.inUse
	default:
		return false
	}
}
