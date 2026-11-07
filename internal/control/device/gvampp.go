package device

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// AMPPAdapter implements DeviceControlAdapter for Grass Valley AMPP
// (Advanced Media Production Platform). AMPP is an IP-based production
// platform that exposes device control via REST API and AMPP control protocol
// over TCP.
//
// AMPP devices include: iDC (Integrated Direction Control), K-Frame,
// Galileo, Mars, and other Grass Valley production switchers, routers,
// and multiviewers.
//
// AMPP REST API endpoints:
//   GET  /ampx/v1/devices          - List devices
//   GET  /ampx/v1/devices/{id}     - Get device details
//   POST /ampx/v1/routers/{id}/xpt - Set crosspoint
//   GET  /ampx/v1/routers/{id}/xpt - Get crosspoint state
type AMPPAdapter struct {
	mu      sync.Mutex
	conn    net.Conn
	config  DeviceConfig
	addr    string
	timeout time.Duration
	logger  *log.Logger
	baseURL string
}

// NewAMPPAdapter creates a new AMPP adapter.
func NewAMPPAdapter(config DeviceConfig, logger *log.Logger) *AMPPAdapter {
	return &AMPPAdapter{
		config:  config,
		addr:    net.JoinHostPort(config.Host, strconv.Itoa(config.Port)),
		timeout: 5 * time.Second,
		logger:  logger,
		baseURL: fmt.Sprintf("http://%s:%d/ampx/v1", config.Host, config.Port),
	}
}

// Connect establishes a TCP connection to the AMPP device.
func (a *AMPPAdapter) Connect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.conn != nil {
		return nil // already connected
	}

	d := net.Dialer{Timeout: a.timeout}
	conn, err := d.DialContext(ctx, "tcp", a.addr)
	if err != nil {
		return fmt.Errorf("ampp connect: %w", err)
	}

	a.conn = conn
	a.logger.Printf("ampp connected to %s", a.addr)
	return nil
}

// Disconnect closes the connection to the AMPP device.
func (a *AMPPAdapter) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.conn != nil {
		err := a.conn.Close()
		a.conn = nil
		if err != nil {
			return fmt.Errorf("ampp disconnect: %w", err)
		}
		a.logger.Println("ampp disconnected")
	}
	return nil
}

// SendCommand sends an AMPP control command to the device.
// Supported commands:
//   - router.take:<src>:<dst>  - Perform a router crosspoint take
//   - router.protect:<src>:<dst> - Protect a crosspoint
//   - router.unprotect:<src>:<dst> - Remove protection
//   - multiviewer.set:<mv>:<input>:<dest> - Set multiviewer input
//   - device.status:<deviceId> - Get device status
func (a *AMPPAdapter) SendCommand(ctx context.Context, command string, params map[string]string) (string, error) {
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()

	if conn == nil {
		return "", fmt.Errorf("ampp: not connected")
	}

	// Build AMPP command payload
	payload := map[string]interface{}{
		"command":  command,
		"params":   params,
		"device":   a.config.DeviceID,
		"timestamp": time.Now().Format(time.RFC3339),
	}

	cmdBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("ampp command marshal: %w", err)
	}

	cmdBytes = append(cmdBytes, '\n')

	_, err = conn.Write(cmdBytes)
	if err != nil {
		a.mu.Lock()
		conn.Close()
		a.conn = nil
		a.mu.Unlock()
		return "", fmt.Errorf("ampp command write: %w", err)
	}

	a.logger.Printf("ampp command: %s params=%v", command, params)
	return "ok", nil
}

// Status returns the current status of the AMPP adapter.
func (a *AMPPAdapter) Status(ctx context.Context) (DeviceStatus, error) {
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
func (a *AMPPAdapter) IsConnected() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conn != nil
}

// Compile-time check that AMPPAdapter implements DeviceControlAdapter.
var _ DeviceControlAdapter = (*AMPPAdapter)(nil)

// AMPPDevice represents an AMPP device discovered via REST API.
type AMPPDevice struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`      // "router", "multiviewer", "switcher", "server"
	Model    string `json:"model"`
	Serial   string `json:"serial"`
	Firmware string `json:"firmware"`
	Status   string `json:"status"`    // "online", "offline", "degraded"
	IP       string `json:"ip"`
	Port     int    `json:"port"`
}

// AMPPCrosspoint represents an AMPP router crosspoint connection.
type AMPPCrosspoint struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Protected   bool   `json:"protected"`
	Timestamp   string `json:"timestamp"`
}

// AMPPClient is a REST client for the AMPP device.
type AMPPClient struct {
	baseURL string
	http    *http.Client
}

// NewAMPPClient creates a new AMPP REST client.
func NewAMPPClient(baseURL string) *AMPPClient {
	return &AMPPClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// ListDevices queries GET /ampx/v1/devices from the AMPP registry.
func (c *AMPPClient) ListDevices(ctx context.Context) ([]AMPPDevice, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/devices", nil)
	if err != nil {
		return nil, fmt.Errorf("ampp list devices request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ampp list devices: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("ampp list devices %d", resp.StatusCode)
	}

	var devices []AMPPDevice
	if err := json.NewDecoder(resp.Body).Decode(&devices); err != nil {
		return nil, fmt.Errorf("ampp decode devices: %w", err)
	}
	return devices, nil
}

// GetCrosspoint queries the router crosspoint state.
func (c *AMPPClient) GetCrosspoint(ctx context.Context, routerID string) ([]AMPPCrosspoint, error) {
	url := fmt.Sprintf("%s/routers/%s/xpt", c.baseURL, routerID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("ampp crosspoint request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ampp crosspoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("ampp crosspoint %d", resp.StatusCode)
	}

	var xpts []AMPPCrosspoint
	if err := json.NewDecoder(resp.Body).Decode(&xpts); err != nil {
		return nil, fmt.Errorf("ampp decode crosspoint: %w", err)
	}
	return xpts, nil
}

// SetCrosspoint sets a router crosspoint connection.
func (c *AMPPClient) SetCrosspoint(ctx context.Context, routerID, source, destination string) error {
	url := fmt.Sprintf("%s/routers/%s/xpt", c.baseURL, routerID)

	payload := map[string]interface{}{
		"source":      source,
		"destination": destination,
		"action":      "take",
		"timestamp":   time.Now().Format(time.RFC3339),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("ampp crosspoint marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("ampp crosspoint request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("ampp crosspoint set: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("ampp crosspoint set %d", resp.StatusCode)
	}
	return nil
}
