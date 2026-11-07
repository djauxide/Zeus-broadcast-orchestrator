package device

import (
	"context"
	"fmt"
	"log"
	"net"
	"strconv"
	"sync"
	"time"
)

// graphicsAdapter implements DeviceControlAdapter for graphics systems
// (character generators, playout graphics) using TCP/IP control.
// This is a scaffold implementation.
type graphicsAdapter struct {
	mu      sync.Mutex
	conn    net.Conn
	config  DeviceConfig
	addr    string
	timeout time.Duration
	logger  *log.Logger
}

// NewGraphicsAdapter creates a new graphics system control adapter.
func NewGraphicsAdapter(config DeviceConfig, logger *log.Logger) *graphicsAdapter {
	return &graphicsAdapter{
		config:  config,
		addr:    net.JoinHostPort(config.Host, strconv.Itoa(config.Port)),
		timeout: 5 * time.Second,
		logger:  logger,
	}
}

// Connect establishes a TCP connection to the graphics system.
func (a *graphicsAdapter) Connect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.conn != nil {
		return nil
	}

	d := net.Dialer{Timeout: a.timeout}
	conn, err := d.DialContext(ctx, "tcp", a.addr)
	if err != nil {
		return fmt.Errorf("graphics connect: %w", err)
	}

	a.conn = conn
	a.logger.Printf("graphics system connected to %s", a.addr)
	return nil
}

// Disconnect closes the connection to the graphics system.
func (a *graphicsAdapter) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.conn != nil {
		err := a.conn.Close()
		a.conn = nil
		if err != nil {
			return fmt.Errorf("graphics disconnect: %w", err)
		}
		a.logger.Println("graphics system disconnected")
	}
	return nil
}

// SendCommand sends a command to the graphics system.
func (a *graphicsAdapter) SendCommand(ctx context.Context, command string, params map[string]string) (string, error) {
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()

	if conn == nil {
		return "", fmt.Errorf("graphics system: not connected")
	}

	cmd := command + "\r\n"
	_, err := conn.Write([]byte(cmd))
	if err != nil {
		a.mu.Lock()
		conn.Close()
		a.conn = nil
		a.mu.Unlock()
		return "", fmt.Errorf("graphics system command write: %w", err)
	}

	a.logger.Printf("graphics command: %s params=%v", command, params)
	return "ok", nil
}

// Status returns the current status of the graphics system adapter.
func (a *graphicsAdapter) Status(ctx context.Context) (DeviceStatus, error) {
	a.mu.Lock()
	connected := a.conn != nil
	a.mu.Unlock()

	return DeviceStatus{
		Connected: connected,
		Online:    connected,
		Device:    a.addr,
	}, nil
}

// IsConnected returns whether the adapter is currently connected.
func (a *graphicsAdapter) IsConnected() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conn != nil
}
