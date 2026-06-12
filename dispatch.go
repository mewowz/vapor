package vapor

import (
	"encoding/binary"
	"log/slog"
	"slices"
)

type Dispatcher struct {
	jobChanMap map[uint64]chan<- Message
	logger     *slog.Logger
}

func NewDispatcher(logger *slog.Logger) *Dispatcher {
	return &Dispatcher{
		jobChanMap: make(map[uint64]chan<- Message),
		logger:     logger,
	}
}

func (d *Dispatcher) DispatchMessage(msgBytes []byte) error {
	rawEmsg := EMsg(binary.LittleEndian.Uint32(msgBytes[:4]))
	isProto := rawEmsg&0x80000000 != 0
	emsg := rawEmsg & 0x7FFFFFFF

	if isProto {
		message, err := NewMsgHeaderPBFromBytes(msgBytes)
		if err != nil {
			d.logger.Debug("got error while deserializing ProtoBuf message from bytes", "err", err)
			return err
		}
		//err = d.dispatchPBMessage(message)
		err = d.writeMessageToCaller(
			message,
			message.header.GetJobidTarget(),
			message.header.GetJobidSource(),
		)
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
		err := d.writeMessageToCaller(
			message,
			message.targetJobID,
			message.sourceJobID,
		)
		return err
	}
}

func (d *Dispatcher) Register(jobID uint64, returnChan chan<- Message) error {
	_, exists := d.jobChanMap[jobID]
	if exists {
		return ErrJobIDAlreadyInDispatcher
	}
	d.jobChanMap[jobID] = returnChan
	return nil
}

func (d *Dispatcher) writeMessageToCaller(
	message Message,
	targetJobID,
	sourceJobID uint64,
) error {
	if targetJobID == DefaultJobID {
		d.logger.Debug("got DefaultJobID; dropping job", "EMsg", message.EMsg())
		return nil
	}
	jobChan, exists := d.jobChanMap[targetJobID]
	if !exists {
		d.logger.Debug(
			"no mapping for message", "EMsg", message.EMsg(),
			"sourceJobID", sourceJobID,
			"targetJobID", targetJobID,
		)
		return ErrNoChanForJob
	}
	select {
	case jobChan <- message:
	default:
		d.logger.Error("blocking on jobChan", "EMsg", message.EMsg(), "targetJobID", targetJobID)
		panic("jobChan is never allowed to block")
	}
	delete(d.jobChanMap, targetJobID)
	return nil
}
