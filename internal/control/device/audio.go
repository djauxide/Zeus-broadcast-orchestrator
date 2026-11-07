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

// audioAdapter implements DeviceControlAdapter for audio devices (mixers,
// processors, etc.) using TCP/IP control.
// This is a scaffold implementation - production implementations would use
// specific protocols (AES67, Dante, etc.).
type audioAdapter struct {
	mu      sync.Mutex
	conn    net.Conn
	config  DeviceConfig
	addr    string
	timeout time.Duration
	logger  *log.Logger
}

// NewAudioAdapter creates a new audio device control adapter.
func NewAudioAdapter(config DeviceConfig, logger *log.Logger) *audioAdapter {
	return &audioAdapter{
		config:  config,
		addr:    net.JoinHostPort(config.Host, strconv.Itoa(config.Port)),
		timeout: 5 * time.Second,
		logger:  logger,
	}
}

// Connect establishes a TCP connection to the audio device.
func (a *audioAdapter) Connect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.conn != nil {
		return nil
	}

	d := net.Dialer{Timeout: a.timeout}
	conn, err := d.DialContext(ctx, "tcp", a.addr)
	if err != nil {
		return fmt.Errorf("audio connect: %w", err)
	}

	a.conn = conn
	a.logger.Printf("audio device connected to %s", a.addr)
	return nil
}

// Disconnect closes the connection to the audio device.
func (a *audioAdapter) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.conn != nil {
		err := a.conn.Close()
		a.conn = nil
		if err != nil {
			return fmt.Errorf("audio disconnect: %w", err)
		}
		a.logger.Println("audio device disconnected")
	}
	return nil
}

// SendCommand sends a command to the audio device.
func (a *audioAdapter) SendCommand(ctx context.Context, command string, params map[string]string) (string, error) {
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()

	if conn == nil {
		return "", fmt.Errorf("audio device: not connected")
	}

	cmd := command + "\r\n"
	_, err := conn.Write([]byte(cmd))
	if err != nil {
		a.mu.Lock()
		conn.Close()
		a.conn = nil
		a.mu.Unlock()
		return "", fmt.Errorf("audio device command write: %w", err)
	}

	a.logger.Printf("audio command: %s params=%v", command, params)
	return "ok", nil
}

// Status returns the current status of the audio device adapter.
func (a *audioAdapter) Status(ctx context.Context) (DeviceStatus, error) {
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
func (a *audioAdapter) IsConnected() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conn != nil
}
