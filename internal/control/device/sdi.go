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

// SDTIAdapter implements DeviceControlAdapter for SDTI (Serial Digital Interface Transport).
// SDTI is used to transport SRT streams over SDI infrastructure.
// SDTI carries SRT packets encapsulated in SDI ancillary data or as separate
// data channels within the SDI stream.
//
// SDTI is used for:
//   - Transporting SRT streams over SDI infrastructure
//   - Carrying metadata and ancillary data alongside video
//   - Interfacing between IP and SDI domains
type SDTIAdapter struct {
	mu      sync.Mutex
	conn    net.Conn
	config  DeviceConfig
	addr    string
	timeout time.Duration
	logger  *log.Logger
}

// NewSDTIAdapter creates a new SDTI device control adapter.
func NewSDTIAdapter(config DeviceConfig, logger *log.Logger) *SDTIAdapter {
	return &SDTIAdapter{
		config:  config,
		addr:    net.JoinHostPort(config.Host, strconv.Itoa(config.Port)),
		timeout: 5 * time.Second,
		logger:  logger,
	}
}

// Connect establishes a TCP connection to the SDTI device.
func (a *SDTIAdapter) Connect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.conn != nil {
		return nil
	}

	d := net.Dialer{Timeout: a.timeout}
	conn, err := d.DialContext(ctx, "tcp", a.addr)
	if err != nil {
		return fmt.Errorf("SDTI connect: %w", err)
	}

	a.conn = conn
	a.logger.Printf("SDTI connected to %s", a.addr)
	return nil
}

// Disconnect closes the connection to the SDTI device.
func (a *SDTIAdapter) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.conn != nil {
		err := a.conn.Close()
		a.conn = nil
		if err != nil {
			return fmt.Errorf("SDTI disconnect: %w", err)
		}
		a.logger.Println("SDTI disconnected")
	}
	return nil
}

// SendCommand sends a command to the SDTI device.
// Supported commands:
//   - srt.load:<streamId>     - Load an SRT stream
//   - srt.play:<streamId>     - Play an SRT stream
//   - srt.stop:<clipId>       - Stop an SRT stream
//   - srt.config:<configId>   - Configure SRT parameters
//   - mapping.add:<src>:<dst> - Add SDI to SRT mapping
//   - mapping.remove:<src>:<dst> - Remove SDI to SRT mapping
func (a *SDTIAdapter) SendCommand(ctx context.Context, command string, params map[string]string) (string, error) {
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()

	if conn == nil {
		return "", fmt.Errorf("SDTI: not connected")
	}

	cmd := command + "\r\n"
	_, err := conn.Write([]byte(cmd))
	if err != nil {
		a.mu.Lock()
		conn.Close()
		a.conn = nil
		a.mu.Unlock()
		return "", fmt.Errorf("SDTI command write: %w", err)
	}

	a.logger.Printf("SDTI command: %s params=%v", command, params)
	return "ok", nil
}

// Status returns the current status of the SDTI adapter.
func (a *SDTIAdapter) Status(ctx context.Context) (DeviceStatus, error) {
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
func (a *SDTIAdapter) IsConnected() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conn != nil
}

// Compile-time check that SDTIAdapter implements DeviceControlAdapter.
var _ DeviceControlAdapter = (*SDTIAdapter)(nil)

// Ensure SDTI adapter is registered by default.
func init() {
	RegisterDeviceAdapter("sdi", func(config DeviceConfig, logger *log.Logger) DeviceControlAdapter {
		return NewSDTIAdapter(config, logger)
	})
}