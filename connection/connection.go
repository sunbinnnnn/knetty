package connection

import (
	"bytes"
	"fmt"
	"github.com/Softwarekang/knet/poll"
	"net"
	"syscall"
	"time"

	"go.uber.org/atomic"
)

var (
	connID atomic.Uint32
)

const (
	netIOTimeout = time.Second // 1s
)

type CloseCallBackFunc func() error

// Connection some connection  operations
type Connection interface {
	// ID for conn
	ID() uint32
	// LocalAddr local address for conn
	LocalAddr() string
	// RemoteAddr remote address for conn
	RemoteAddr() string
	// ReadTimeout timeout for read
	ReadTimeout() time.Duration
	// SetReadTimeout setup read timeout
	SetReadTimeout(time.Duration)
	// WriteTimeout timeout for write
	WriteTimeout() time.Duration
	// SetWriteTimeout setup write timeout
	SetWriteTimeout(time.Duration)
	// Read will return length n bytes
	Read(n int) ([]byte, error)

	// Len will return conn readable data size
	Len() int
	// Close will interrupt conn
	Close()
}

// Conn wrapped net.conn with fd、remote sa
type Conn interface {
	net.Conn

	// FD will return conn fd
	FD() int

	// RemoteSocketAddr will return conn remote sa
	RemoteSocketAddr() syscall.Sockaddr
}

type wrappedConn struct {
	net.Conn
	remoteSocketAddr syscall.Sockaddr
	fd               int
}

// NewWrappedConn .
func NewWrappedConn(conn net.Conn) (*wrappedConn, error) {
	tcpConn := conn.(*net.TCPConn)
	file, err := tcpConn.File()
	if err != nil {
		return nil, err
	}

	tcpAddr := conn.RemoteAddr().(*net.TCPAddr)
	remoteScoketAddr, err := ipToSockaddrInet4(tcpAddr.IP, tcpAddr.Port)
	if err != nil {
		panic("")
	}

	return &wrappedConn{
		Conn:             conn,
		fd:               int(file.Fd()),
		remoteSocketAddr: remoteScoketAddr,
	}, nil
}

// FD .
func (w *wrappedConn) FD() int {
	return w.fd
}

// RemoteSocketAddr .
func (w *wrappedConn) RemoteSocketAddr() syscall.Sockaddr {
	return w.remoteSocketAddr
}

type kNetConn struct {
	id               uint32
	fd               int
	readTimeOut      *atomic.Duration
	writeTimeOut     *atomic.Duration
	remoteSocketAddr syscall.Sockaddr
	localAddress     string
	remoteAddress    string
	poller           poll.Poll
	inputBuffer      bytes.Buffer
	closeCallBackFn  CloseCallBackFunc
	waitBufferSize   atomic.Int64
	waitBufferChan   chan struct{}
	close            atomic.Int32
}

// Register register in poller
func (c *kNetConn) Register() error {
	if err := c.poller.Register(&poll.NetFileDesc{
		FD: c.fd,
		NetPollListener: poll.NetPollListener{
			OnRead:      c.OnRead,
			OnInterrupt: c.OnInterrupt,
		},
	}, poll.Read); err != nil {
		return err
	}
	return nil
}

// OnRead refactor for conn
func (c *kNetConn) OnRead() error {
	// 0.25m bytes
	bytes := make([]byte, 256)
	n, err := syscall.Read(c.fd, bytes)
	if err != nil {
		if err != syscall.EAGAIN {
			return err
		}
	}

	fmt.Printf("buffer input:%s\n", string(bytes))
	c.inputBuffer.Write(bytes[:n])
	waitBufferSize := c.waitBufferSize.Load()
	if waitBufferSize > 0 && int64(c.inputBuffer.Len()) > waitBufferSize {
		c.waitBufferChan <- struct{}{}
	}
	return nil
}

// OnInterrupt refactor for conn
func (c *kNetConn) OnInterrupt() error {
	if err := c.poller.Register(&poll.NetFileDesc{
		FD: c.fd,
	}, poll.DeleteRead); err != nil {
		return err
	}

	if c.closeCallBackFn != nil {
		c.closeCallBackFn()
	}
	c.close.Store(1)
	return nil
}
