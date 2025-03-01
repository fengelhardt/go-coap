package udp

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"

	coapNet "github.com/plgd-dev/go-coap/v2/net"
	"github.com/plgd-dev/go-coap/v2/udp/client"
	"github.com/plgd-dev/go-coap/v2/udp/message/pool"
)

type EventFunc = func()

type Session struct {
	connection     *coapNet.UDPConn
	raddr          *net.UDPAddr
	maxMessageSize int
	closeSocket    bool

	mutex   sync.Mutex
	onClose []EventFunc

	cancel context.CancelFunc
	ctx    atomic.Value

	doneCtx    context.Context
	doneCancel context.CancelFunc
}

func NewSession(
	ctx context.Context,
	connection *coapNet.UDPConn,
	raddr *net.UDPAddr,
	maxMessageSize int,
	closeSocket bool,
	doneCtx context.Context,
) *Session {
	ctx, cancel := context.WithCancel(ctx)

	doneCtx, doneCancel := context.WithCancel(ctx)
	s := &Session{
		cancel:         cancel,
		connection:     connection,
		raddr:          raddr,
		maxMessageSize: maxMessageSize,
		closeSocket:    closeSocket,
		doneCtx:        doneCtx,
		doneCancel:     doneCancel,
	}
	s.ctx.Store(&ctx)
	return s
}

// SetContextValue stores the value associated with key to context of connection.
func (s *Session) SetContextValue(key interface{}, val interface{}) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	ctx := context.WithValue(s.Context(), key, val)
	s.ctx.Store(&ctx)
}

// Done signalizes that connection is not more processed.
func (s *Session) Done() <-chan struct{} {
	return s.doneCtx.Done()
}

func (s *Session) AddOnClose(f EventFunc) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.onClose = append(s.onClose, f)
}

func (s *Session) popOnClose() []EventFunc {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	tmp := s.onClose
	s.onClose = nil
	return tmp
}

func (s *Session) close() error {
	defer s.doneCancel()
	for _, f := range s.popOnClose() {
		f()
	}
	if s.closeSocket {
		return s.connection.Close()
	}
	return nil
}

func (s *Session) Close() error {
	s.cancel()
	return nil
}

func (s *Session) Context() context.Context {
	return *s.ctx.Load().(*context.Context)
}

func (s *Session) WriteMessage(req *pool.Message) error {
	data, err := req.Marshal()
	if err != nil {
		return fmt.Errorf("cannot marshal: %w", err)
	}
	return s.connection.WriteWithContext(req.Context(), s.raddr, data)
}

func (s *Session) Run(cc *client.ClientConn) (err error) {
	defer func() {
		err1 := s.Close()
		if err == nil {
			err = err1
		}
		err1 = s.close()
		if err == nil {
			err = err1
		}
	}()
	m := make([]byte, s.maxMessageSize)
	for {
		buf := m
		n, _, err := s.connection.ReadWithContext(s.Context(), buf)
		if err != nil {
			return err
		}
		buf = buf[:n]
		err = cc.Process(buf)
		if err != nil {
			return err
		}
	}
}

func (s *Session) MaxMessageSize() int {
	return s.maxMessageSize
}

func (s *Session) RemoteAddr() net.Addr {
	return s.raddr
}
