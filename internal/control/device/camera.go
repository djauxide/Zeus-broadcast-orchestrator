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

// cameraAdapter implements DeviceControlAdapter for VISCA PTZ cameras.
// VISCA is a serial control protocol used by Sony and other PTZ cameras.
// Commands are sent as hex bytes over TCP/IP (via VISCA-over-IP).
type cameraAdapter struct {
	mu      sync.Mutex
	conn    net.Conn
	config  DeviceConfig
	addr    string
	timeout time.Duration
	logger  *log.Logger
}

// NewCameraAdapter creates a new VISCA camera control adapter.
func NewCameraAdapter(config DeviceConfig, logger *log.Logger) *cameraAdapter {
	return &cameraAdapter{
		config:  config,
		addr:    net.JoinHostPort(config.Host, strconv.Itoa(config.Port)),
		timeout: 5 * time.Second,
		logger:  logger,
	}
}

// Connect establishes a TCP connection to the VISCA camera.
func (a *cameraAdapter) Connect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.conn != nil {
		return nil // already connected
	}

	d := net.Dialer{Timeout: a.timeout}
	conn, err := d.DialContext(ctx, "tcp", a.addr)
	if err != nil {
		return fmt.Errorf("camera connect: %w", err)
	}

	a.conn = conn
	a.logger.Printf("camera connected to %s", a.addr)
	return nil
}

// Disconnect closes the connection to the camera.
func (a *cameraAdapter) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.conn != nil {
		err := a.conn.Close()
		a.conn = nil
		if err != nil {
			return fmt.Errorf("camera disconnect: %w", err)
		}
		a.logger.Println("camera disconnected")
	}
	return nil
}

// SendCommand sends a VISCA command to the camera.
// VISCA commands are hex byte sequences (e.g., "81 01 06 04 03 02 FF" for zoom tele).
func (a *cameraAdapter) SendCommand(ctx context.Context, command string, params map[string]string) (string, error) {
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()

	if conn == nil {
		return "", fmt.Errorf("camera: not connected")
	}

	// Parse hex command bytes
	cmdBytes, err := parseHexCommand(command)
	if err != nil {
		return "", fmt.Errorf("camera command parse: %w", err)
	}

	_, err = conn.Write(cmdBytes)
	if err != nil {
		a.mu.Lock()
		conn.Close()
		a.conn = nil
		a.mu.Unlock()
		return "", fmt.Errorf("camera command write: %w", err)
	}

	a.logger.Printf("camera command: %s params=%v", command, params)
	return "ok", nil
}

// Status returns the current status of the camera adapter.
func (a *cameraAdapter) Status(ctx context.Context) (DeviceStatus, error) {
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
func (a *cameraAdapter) IsConnected() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conn != nil
}

// parseHexCommand parses a hex command string like "81 01 06 04 03 02 FF".
func parseHexCommand(cmd string) ([]byte, error) {
	parts := splitHex(cmd)
	bytes := make([]byte, 0, len(parts))
	for _, p := range parts {
		b, err := strconv.ParseUint(p, 16, 8)
		if err != nil {
			return nil, fmt.Errorf("invalid hex byte: %s", p)
		}
		bytes = append(bytes, byte(b))
	}
	return bytes, nil
}

// splitHex splits a hex command string into byte strings.
func splitHex(cmd string) []string {
	var result []string
	current := ""
	for _, c := range cmd {
		if c == ' ' || c == '\t' {
			if current != "" {
				result = append(result, current)
				current = ""
			}
		} else {
			current += string(c)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}
