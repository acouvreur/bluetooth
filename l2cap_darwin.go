package bluetooth

import (
	"errors"
	"io"
	"sync"
	"time"

	"github.com/tinygo-org/cbgo"
)

// L2CAPPSM represents an L2CAP Protocol/Service Multiplexer identifier.
type L2CAPPSM = uint16

// l2capResult is used internally to communicate the result of an L2CAP channel
// open operation from the delegate callback to the calling goroutine.
type l2capResult struct {
	channel cbgo.L2CAPChannel
	err     error
}

// L2CAPConn represents an L2CAP Connection-Oriented Channel connection.
// It implements io.ReadWriteCloser for bidirectional communication.
type L2CAPConn struct {
	channel cbgo.L2CAPChannel
	device  Device
	mu      sync.Mutex
	closed  bool
}

// Compile-time check that L2CAPConn implements io.ReadWriteCloser.
var _ io.ReadWriteCloser = (*L2CAPConn)(nil)

// OpenL2CAPChannel opens an L2CAP Connection-Oriented Channel to the
// connected peripheral. The PSM (Protocol/Service Multiplexer) identifies the
// L2CAP service to connect to on the remote device.
//
// The device must already be connected via Connect before calling this method.
func (d Device) OpenL2CAPChannel(psm L2CAPPSM) (*L2CAPConn, error) {
	ch := make(chan l2capResult, 1)
	d.l2capChan = ch
	defer func() { d.l2capChan = nil }()

	d.prph.OpenL2CAPChannel(psm)

	select {
	case result := <-ch:
		if result.err != nil {
			return nil, result.err
		}
		return &L2CAPConn{
			channel: result.channel,
			device:  d,
		}, nil
	case <-time.NewTimer(30 * time.Second).C:
		return nil, errors.New("bluetooth: timeout on OpenL2CAPChannel")
	}
}

// Read reads data from the L2CAP channel. It blocks until data is available
// or the channel is closed.
func (c *L2CAPConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	for {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return 0, io.ErrClosedPipe
		}
		c.mu.Unlock()

		n := c.channel.Read(p)
		if n < 0 {
			return 0, errors.New("bluetooth: L2CAP read error")
		}
		if n > 0 {
			return n, nil
		}

		// No data available yet, wait briefly before retrying.
		time.Sleep(1 * time.Millisecond)
	}
}

// Write writes data to the L2CAP channel. It blocks until all data has been
// written or the channel is closed.
func (c *L2CAPConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	total := 0
	for total < len(p) {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return total, io.ErrClosedPipe
		}
		c.mu.Unlock()

		n := c.channel.Write(p[total:])
		if n < 0 {
			return total, errors.New("bluetooth: L2CAP write error")
		}
		if n > 0 {
			total += n
			continue
		}

		// Output stream not ready yet, wait briefly before retrying.
		time.Sleep(1 * time.Millisecond)
	}
	return total, nil
}

// Close closes the L2CAP channel.
func (c *L2CAPConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}
	c.closed = true
	c.channel.Close()
	return nil
}

// PSM returns the Protocol/Service Multiplexer identifier for this channel.
func (c *L2CAPConn) PSM() L2CAPPSM {
	return c.channel.PSM()
}
