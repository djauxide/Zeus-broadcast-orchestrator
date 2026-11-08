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

// CerebrumAdapter implements DeviceControlAdapter for EVS Cerebrum
// production servers. Cerebrum is a live production server platform used
// for playout, replay, and graphics. It exposes device control via REST API
// and TCP control protocol.
//
// Devices include:
//   - Production server (playout)
//   - Replay server (slow motion)
//   - Graphics server (CG)
//   - Router (crosspoint control)
//   - Multiviewer
//
// Cerebrum REST API endpoints:
//   GET  /cerebrum/v1/devices          - List devices
//   GET  /cerebrum/v1/devices/{id}     - Get device details
//   POST /cerebrum/v1/playout/load     - Load a clip
//   POST /cerebrum/v1/playout/play     - Play a clip
//   POST /cerebrum/v1/playout/stop     - Stop a clip
//   POST /cerebrum/v1/route            - Set crosspoint route
//   POST /cerebrum/v1/graphics/load    - Load graphic
//   POST /cerebrum/v1/graphics/show    - Show graphic
type CerebrumAdapter struct {
	mu      sync.Mutex
	conn    net.Conn
	config  DeviceConfig
	addr    string
	timeout time.Duration
	logger  *log.Logger
	baseURL string
	http    *http.Client
}

// NewCerebrumAdapter creates a new Cerebrum device control adapter.
func NewCerebrumAdapter(config DeviceConfig, logger *log.Logger) *CerebrumAdapter {
	return &CerebrumAdapter{
		config:   config,
		addr:     net.JoinHostPort(config.Host, strconv.Itoa(config.Port)),
		timeout:  5 * time.Second,
		logger:   logger,
		baseURL:  fmt.Sprintf("http://%s:%d/cerebrum/v1", config.Host, config.Port),
		http:     &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Connect establishes a TCP connection to the Cerebrum device.
func (a *CerebrumAdapter) Connect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.conn != nil {
		return nil // already connected
	}

	d := net.Dialer{Timeout: a.timeout}
	conn, err := d.DialContext(ctx, "tcp", a.addr)
	if err != nil {
		return fmt.Errorf("cerebrum connect: %w", err)
	}

	a.conn = conn
	a.logger.Printf("cerebrum connected to %s", a.addr)
	return nil
}

// Disconnect closes the connection to the Cerebrum device.
func (a *CerebrumAdapter) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.conn != nil {
		err := a.conn.Close()
		a.conn = nil
		if err != nil {
			return fmt.Errorf("cerebrum disconnect: %w", err)
		}
		a.logger.Println("cerebrum disconnected")
	}
	return nil
}

// SendCommand sends a Cerebrum command to the device.
// Supported commands:
//   - playout.load:<clipId>     - Load a clip into playout
//   - playout.play:<clipId>     - Play a clip
//   - playout.stop:<clipId>     - Stop a clip
//   - replay.markIn:<clipId>    - Mark in point for replay
//   - replay.markOut:<clipId>   - Mark out point for replay
//   - graphics.load:<gfxId>     - Load a graphic
//   - graphics.show:<gfxId>     - Show a graphic
//   - route:<src>:<dst>         - Route source to destination
func (a *CerebrumAdapter) SendCommand(ctx context.Context, command string, params map[string]string) (string, error) {
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()

	if conn == nil {
		return "", fmt.Errorf("cerebrum: not connected")
	}

	// Build Cerebrum command payload
	payload := map[string]interface{}{
		"command":  command,
		"params":   params,
		"device":   a.config.DeviceID,
		"timestamp": time.Now().Format(time.RFC3339),
	}

	cmdBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("cerebrum command marshal: %w", err)
	}

	cmdBytes = append(cmdBytes, '\n')

	_, err = conn.Write(cmdBytes)
	if err != nil {
		a.mu.Lock()
		conn.Close()
		a.conn = nil
		a.mu.Unlock()
		return "", fmt.Errorf("cerebrum command write: %w", err)
	}

	a.logger.Printf("cerebrum command: %s params=%v", command, params)
	return "ok", nil
}

// Status returns the current status of the Cerebrum adapter.
func (a *CerebrumAdapter) Status(ctx context.Context) (DeviceStatus, error) {
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
func (a *CerebrumAdapter) IsConnected() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conn != nil
}

// Compile-time check that CerebrumAdapter implements DeviceControlAdapter.
var _ DeviceControlAdapter = (*CerebrumAdapter)(nil)

// Ensure Cerebrum adapter is registered by default.
func init() {
	RegisterDeviceAdapter("cerebrum", func(config DeviceConfig, logger *log.Logger) DeviceControlAdapter {
		return NewCerebrumAdapter(config, logger)
	})
}

// CerebrumDevice represents a Cerebrum device discovered via REST API.
type CerebrumDevice struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`      // "production-server", "replay-server", "graphics-server"
	Model    string `json:"model"`
	Serial   string `json:"serial"`
	Firmware string `json:"firmware"`
	Status   string `json:"status"`    // "online", "offline", "maintenance"
	IP       string `json:"ip"`
	Port     int    `json:"port"`
}

// CerebrumClient is a REST client for the Cerebrum server.
type CerebrumClient struct {
	baseURL string
	http    *http.Client
}

// NewCerebrumClient creates a new Cerebrum REST client.
func NewCerebrumClient(baseURL string) *CerebrumClient {
	return &CerebrumClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// ListDevices queries GET /cerebrum/v1/devices from the Cerebrum server.
func (c *CerebrumClient) ListDevices(ctx context.Context) ([]CerebrumDevice, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/devices", nil)
	if err != nil {
		return nil, fmt.Errorf("cerebrum list devices request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cerebrum list devices: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("cerebrum list devices %d", resp.StatusCode)
	}

	var devices []CerebrumDevice
	if err := json.NewDecoder(resp.Body).Decode(&devices); err != nil {
		return nil, fmt.Errorf("cerebrum decode devices: %w", err)
	}
	return devices, nil
}

// GetDevice queries GET /cerebrum/v1/devices/{id}.
func (c *CerebrumClient) GetDevice(ctx context.Context, deviceID string) (*CerebrumDevice, error) {
	url := fmt.Sprintf("%s/devices/%s", c.baseURL, deviceID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("cerebrum get device request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cerebrum get device: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("cerebrum get device %d", resp.StatusCode)
	}

	var device CerebrumDevice
	if err := json.NewDecoder(resp.Body).Decode(&device); err != nil {
		return nil, fmt.Errorf("cerebrum decode device: %w", err)
	}
	return &device, nil
}

// LoadClip sends a load command to the Cerebrum production server.
func (c *CerebrumClient) LoadClip(ctx context.Context, clipID string) error {
	url := fmt.Sprintf("%s/cerebrum/v1/playout/load", c.baseURL)

	payload := map[string]interface{}{
		"clipId":    clipID,
		"timestamp": time.Now().Format(time.RFC3339),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("cerebrum load clip marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("cerebrum load clip request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("cerebrum load clip: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("cerebrum load clip %d", resp.StatusCode)
	}
	return nil
}

// PlayClip sends a play command to the Cerebrum server.
func (c *CerebrumClient) PlayClip(ctx context.Context, clipID string) error {
	url := fmt.Sprintf("%s/cerebrum/v1/playout/play", c.baseURL)

	payload := map[string]interface{}{
		"clipId":    clipID,
		"timestamp": time.Now().Format(time.RFC3339),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("cerebrum play clip marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("cerebrum play clip request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("cerebrum play clip: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("cerebrum play clip %d", resp.StatusCode)
	}
	return nil
}

// SetRoute sends a route command to the Cerebrum router.
func (c *CerebrumClient) SetRoute(ctx context.Context, source, destination string) error {
	url := fmt.Sprintf("%s/cerebrum/v1/route", c.baseURL)

	payload := map[string]interface{}{
		"source":      source,
		"destination": destination,
		"action":      "take",
		"timestamp":   time.Now().Format(time.RFC3339),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("cerebrum route marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("cerebrum route request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("cerebrum route set: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("cerebrum route set %d", resp.StatusCode)
	}
	return nil
}

// LoadGraphic sends a load graphic command to the Cerebrum server.
func (c *CerebrumClient) LoadGraphic(ctx context.Context, graphicID string) error {
	url := fmt.Sprintf("%s/cerebrum/v1/graphics/load", c.baseURL)

	payload := map[string]interface{}{
		"graphicId": graphicID,
		"timestamp": time.Now().Format(time.RFC3339),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("cerebrum load graphic marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("cerebrum load graphic request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("cerebrum load graphic: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("cerebrum load graphic %d", resp.StatusCode)
	}
	return nil
}
