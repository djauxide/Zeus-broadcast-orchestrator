package emberplus

import (
	"context"
	"fmt"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/zeus-broadcast/zeus-kernel/internal/control"
)

// emberplusAdapter implements control.Adapter for Ember+ router control.
// Ember+ is a tree-based control protocol used by Lawo and other broadcast
// equipment manufacturers. It exposes devices as a tree of nodes with
// parameters that can be read and written.
//
// This is a scaffold implementation. A production implementation would need
// to implement the full Ember+ protocol (tree structure, parameter types,
// event subscriptions, etc.) over TCP/IP.
//
// See https://launch.lawo.com/EmberPlus/ for the Ember+ specification.
type emberplusAdapter struct {
	mu      sync.Mutex
	conn    net.Conn
	config  control.RouterConfig
	addr    string
	timeout time.Duration
	logger  *log.Logger
}

// NewAdapter creates a new Ember+ adapter.
func NewAdapter(config control.RouterConfig, logger *log.Logger) *emberplusAdapter {
	return &emberplusAdapter{
		config:  config,
		addr:    net.JoinHostPort(config.Host, strconv.Itoa(config.Port)),
		timeout: 5 * time.Second,
		logger:  logger,
	}
}

// Connect establishes a TCP connection to the Ember+ device.
func (a *emberplusAdapter) Connect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.conn != nil {
		return nil // already connected
	}

	d := net.Dialer{Timeout: a.timeout}
	conn, err := d.DialContext(ctx, "tcp", a.addr)
	if err != nil {
		return fmt.Errorf("emberplus connect: %w", err)
	}

	a.conn = conn
	a.logger.Printf("emberplus connected to %s", a.addr)
	return nil
}

// Disconnect closes the connection to the router.
func (a *emberplusAdapter) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.conn != nil {
		err := a.conn.Close()
		a.conn = nil
		if err != nil {
			return fmt.Errorf("emberplus disconnect: %w", err)
		}
		a.logger.Println("emberplus disconnected")
	}
	return nil
}

// Route establishes a crosspoint connection from source to destination.
// In Ember+, this typically involves setting a parameter on the router
// device tree to connect the source to the destination.
func (a *emberplusAdapter) Route(ctx context.Context, source, destination string) error {
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()

	if conn == nil {
		return fmt.Errorf("emberplus: not connected")
	}

	// Scaffold: In a real implementation, this would construct an Ember+
	// Set command to set the crosspoint parameter on the router device
	// tree. The format depends on the specific router model.
	cmd := fmt.Sprintf("emberplus:set:%s->%s\r\n", source, destination)
	_, err := conn.Write([]byte(cmd))
	if err != nil {
		a.mu.Lock()
		conn.Close()
		a.conn = nil
		a.mu.Unlock()
		return fmt.Errorf("emberplus route write: %w", err)
	}

	a.logger.Printf("emberplus route: source=%s dest=%s", source, destination)
	return nil
}

// Unroute removes a crosspoint connection.
func (a *emberplusAdapter) Unroute(ctx context.Context, source, destination string) error {
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()

	if conn == nil {
		return fmt.Errorf("emberplus: not connected")
	}

	cmd := fmt.Sprintf("emberplus:unset:%s->%s\r\n", source, destination)
	_, err := conn.Write([]byte(cmd))
	if err != nil {
		a.mu.Lock()
		conn.Close()
		a.conn = nil
		a.mu.Unlock()
		return fmt.Errorf("emberplus unroute write: %w", err)
	}

	a.logger.Printf("emberplus unroute: source=%s dest=%s", source, destination)
	return nil
}

// Status returns the current status of the adapter and router.
func (a *emberplusAdapter) Status(ctx context.Context) (control.AdapterStatus, error) {
	a.mu.Lock()
	connected := a.conn != nil
	a.mu.Unlock()

	return control.AdapterStatus{
		Connected: connected,
		Router:    a.addr,
	}, nil
}

// IsConnected returns whether the adapter is currently connected.
func (a *emberplusAdapter) IsConnected() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conn != nil
}
