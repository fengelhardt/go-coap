package pool

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/plgd-dev/go-coap/v2/message"
	"github.com/plgd-dev/go-coap/v2/message/pool"
	tcp "github.com/plgd-dev/go-coap/v2/tcp/message"
)

const maxMessagePool = 10240
const maxMessageBufferSize = 2048

var (
	currentMessagesInPool int32
	messagePool           sync.Pool
)

type Message struct {
	*pool.Message

	//local vars
	rawData        []byte
	rawMarshalData []byte

	ctx        context.Context
	isModified bool
}

// Reset clear message for next reuse
func (r *Message) Reset() {
	r.Message.Reset()
	if cap(r.rawData) > maxMessageBufferSize {
		r.rawData = make([]byte, 256)
	}
	if cap(r.rawMarshalData) > maxMessageBufferSize {
		r.rawMarshalData = make([]byte, 256)
	}
	r.isModified = false
}

func (r *Message) Context() context.Context {
	return r.ctx
}

func (r *Message) IsModified() bool {
	return r.isModified || r.Message.IsModified()
}

func (r *Message) Unmarshal(data []byte) (int, error) {
	if len(r.rawData) < len(data) {
		r.rawData = append(r.rawData, make([]byte, len(data)-len(r.rawData))...)
	}
	copy(r.rawData, data)
	r.rawData = r.rawData[:len(data)]
	m := &tcp.Message{
		Options: make(message.Options, 0, 16),
	}

	n, err := m.Unmarshal(r.rawData)
	if err != nil {
		return n, err
	}
	r.Message.SetCode(m.Code)
	r.Message.SetToken(m.Token)
	r.Message.ResetOptionsTo(m.Options)
	if len(m.Payload) > 0 {
		r.Message.SetBody(bytes.NewReader(m.Payload))
	}
	return n, err
}

func (r *Message) Marshal() ([]byte, error) {
	m := tcp.Message{
		Code:    r.Code(),
		Token:   r.Message.Token(),
		Options: r.Message.Options(),
	}
	payload, err := r.ReadBody()
	if err != nil {
		return nil, err
	}
	m.Payload = payload
	size, err := m.Size()
	if err != nil {
		return nil, err
	}
	if len(r.rawMarshalData) < size {
		r.rawMarshalData = append(r.rawMarshalData, make([]byte, size-len(r.rawMarshalData))...)
	}
	n, err := m.MarshalTo(r.rawMarshalData)
	if err != nil {
		return nil, err
	}
	r.rawMarshalData = r.rawMarshalData[:n]
	return r.rawMarshalData, nil
}

// AcquireMessage returns an empty Message instance from Message pool.
//
// The returned Message instance may be passed to ReleaseMessage when it is
// no longer needed. This allows Message recycling, reduces GC pressure
// and usually improves performance.
func AcquireMessage(ctx context.Context) *Message {
	v := messagePool.Get()
	if v == nil {
		return &Message{
			Message:        pool.NewMessage(),
			rawData:        make([]byte, 256),
			rawMarshalData: make([]byte, 256),
			ctx:            ctx,
		}
	}
	atomic.AddInt32(&currentMessagesInPool, -1)
	r := v.(*Message)
	r.ctx = ctx
	return r
}

// ReleaseMessage returns req acquired via AcquireMessage to Message pool.
//
// It is forbidden accessing req and/or its' members after returning
// it to Message pool.
func ReleaseMessage(req *Message) {
	v := atomic.LoadInt32(&currentMessagesInPool)
	if v >= maxMessagePool {
		return
	}
	atomic.AddInt32(&currentMessagesInPool, 1)
	req.Reset()
	req.ctx = nil
	messagePool.Put(req)
}

// ConvertFrom converts common message to pool message.
func ConvertFrom(m *message.Message) (*Message, error) {
	if m.Context == nil {
		return nil, fmt.Errorf("invalid context")
	}
	r := AcquireMessage(m.Context)
	r.SetCode(m.Code)
	r.ResetOptionsTo(m.Options)
	r.SetBody(m.Body)
	r.SetToken(m.Token)
	return r, nil
}

// ConvertTo converts pool message to common message.
func ConvertTo(m *Message) (*message.Message, error) {
	opts, err := m.Options().Clone()
	if err != nil {
		return nil, err
	}
	var body io.ReadSeeker
	if m.Body() != nil {
		payload, err := m.ReadBody()
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(payload)
	}
	return &message.Message{
		Context: m.Context(),
		Code:    m.Code(),
		Token:   m.Token(),
		Body:    body,
		Options: opts,
	}, nil
}
