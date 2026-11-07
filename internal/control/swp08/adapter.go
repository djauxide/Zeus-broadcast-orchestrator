package swp08

import (
	"context"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zeus-broadcast/zeus-kernel/internal/control"
)

// swp08Adapter implements control.Adapter for SW-P-08 router control.
// SW-P-08 is a serial/IP protocol used by Grass Valley routers for router
// control. Commands are sent as ASCII text over TCP.
//
// SW-P-08 command format:
//   <STX><source><dest><CR><ETX>
//   Where <STX> = 0x02, <CR> = 0x0D, <ETX> = 0x03
type swp08Adapter struct {
	mu      sync.Mutex
	conn    net.Conn
	config  control.RouterConfig
	addr    string
	timeout time.Duration
	logger  *log.Logger
}

// NewAdapter creates a new SW-P-08 adapter.
func NewAdapter(config control.RouterConfig, logger *log.Logger) *swp08Adapter {
	return &swp08Adapter{
		config:  config,
		addr:    net.JoinHostPort(config.Host, strconv.Itoa(config.Port)),
		timeout: 5 * time.Second,
		logger:  logger,
	}
}

// Connect establishes a TCP connection to the SW-P-08 router.
func (a *swp08Adapter) Connect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.conn != nil {
		return nil // already connected
	}

	d := net.Dialer{Timeout: a.timeout}
	conn, err := d.DialContext(ctx, "tcp", a.addr)
	if err != nil {
		return fmt.Errorf("swp08 connect: %w", err)
	}

	a.conn = conn
	a.logger.Printf("swp08 connected to %s", a.addr)
	return nil
}

// Disconnect closes the connection to the router.
func (a *swp08Adapter) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.conn != nil {
		err := a.conn.Close()
		a.conn = nil
		if err != nil {
			return fmt.Errorf("swp08 disconnect: %w", err)
		}
		a.logger.Println("swp08 disconnected")
	}
	return nil
}

// Route establishes a crosspoint connection from source to destination.
func (a *swp08Adapter) Route(ctx context.Context, source, destination string) error {
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()

	if conn == nil {
		return fmt.Errorf("swp08: not connected")
	}

	cmd := fmt.Sprintf("\x02%s%s\r\x03", source, destination)
	_, err := conn.Write([]byte(cmd))
	if err != nil {
		a.mu.Lock()
		conn.Close()
		a.conn = nil
		a.mu.Unlock()
		return fmt.Errorf("swp08 route write: %w", err)
	}

	a.logger.Printf("swp08 route: source=%s dest=%s", source, destination)
	return nil
}

// Unroute removes a crosspoint connection.
func (a *swp08Adapter) Unroute(ctx context.Context, source, destination string) error {
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()

	if conn == nil {
		return fmt.Errorf("swp08: not connected")
	}

	cmd := fmt.Sprintf("\x02%s%s\r\x03", source, destination)
	_, err := conn.Write([]byte(cmd))
	if err != nil {
		a.mu.Lock()
		conn.Close()
		a.conn = nil
		a.mu.Unlock()
		return fmt.Errorf("swp08 unroute write: %w", err)
	}

	a.logger.Printf("swp08 unroute: source=%s dest=%s", source, destination)
	return nil
}

// Status returns the current status of the adapter and router.
func (a *swp08Adapter) Status(ctx context.Context) (control.AdapterStatus, error) {
	a.mu.Lock()
	connected := a.conn != nil
	a.mu.Unlock()

	return control.AdapterStatus{
		Connected: connected,
		Router:    a.addr,
	}, nil
}

// IsConnected returns whether the adapter is currently connected.
func (a *swp08Adapter) IsConnected() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conn != nil
}

// parseResponse parses an SW-P-08 response packet.
func parseResponse(data []byte) (string, error) {
	if len(data) < 5 {
		return "", fmt.Errorf("swp08: response too short: %d bytes", len(data))
	}
	if data[0] != 0x02 {
		return "", fmt.Errorf("swp08: missing STX byte")
	}
	if data[len(data)-1] != 0x03 {
		return "", fmt.Errorf("swp08: missing ETX byte")
	}
	return strings.TrimSpace(string(data[1 : len(data)-1])), nil
}
