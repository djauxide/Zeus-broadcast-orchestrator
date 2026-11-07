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

// NEPAdapter implements DeviceControlAdapter for NEP Group facilities.
// NEP Group is a major broadcast services company with facilities worldwide.
// The NEP adapter connects Zeus to NEP facilities for facility management,
// device control, routing, and show control via their control API.
//
// NEP facility control endpoints:
//   GET  /nep/v1/facilities        - List facilities
//   GET  /nep/v1/facilities/{id}   - Get facility details
//   POST /nep/v1/facilities/{id}/route - Set route
//   GET  /nep/v1/facilities/{id}/route - Get route state
//   POST /nep/v1/facilities/{id}/command - Send device command
type NEPAdapter struct {
	mu      sync.Mutex
	conn    net.Conn
	config  DeviceConfig
	addr    string
	timeout time.Duration
	logger  *log.Logger
	baseURL string
}

// NewNEPAdapter creates a new NEP adapter.
func NewNEPAdapter(config DeviceConfig, logger *log.Logger) *NEPAdapter {
	return &NEPAdapter{
		config:  config,
		addr:    net.JoinHostPort(config.Host, strconv.Itoa(config.Port)),
		timeout: 5 * time.Second,
		logger:  logger,
		baseURL: fmt.Sprintf("http://%s:%d/nep/v1", config.Host, config.Port),
	}
}

// Connect establishes a TCP connection to the NEP facility control server.
func (a *NEPAdapter) Connect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.conn != nil {
		return nil // already connected
	}

	d := net.Dialer{Timeout: a.timeout}
	conn, err := d.DialContext(ctx, "tcp", a.addr)
	if err != nil {
		return fmt.Errorf("nep connect: %w", err)
	}

	a.conn = conn
	a.logger.Printf("nep connected to %s", a.addr)
	return nil
}

// Disconnect closes the connection to the NEP facility.
func (a *NEPAdapter) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.conn != nil {
		err := a.conn.Close()
		a.conn = nil
		if err != nil {
			return fmt.Errorf("nep disconnect: %w", err)
		}
		a.logger.Println("nep disconnected")
	}
	return nil
}

// SendCommand sends a NEP facility control command to the device.
// Supported commands:
//   - facility.route:<src>:<dst>  - Perform a facility route
//   - facility.protect:<src>:<dst> - Protect a route
//   - facility.unprotect:<src>:<dst> - Remove protection
//   - device.command:<deviceId>:<cmd> - Send device command
//   - facility.status:<facilityId> - Get facility status
func (a *NEPAdapter) SendCommand(ctx context.Context, command string, params map[string]string) (string, error) {
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()

	if conn == nil {
		return "", fmt.Errorf("nep: not connected")
	}

	// Build NEP command payload
	payload := map[string]interface{}{
		"command":  command,
		"params":   params,
		"device":   a.config.DeviceID,
		"facility": a.config.FacilityID,
		"timestamp": time.Now().Format(time.RFC3339),
	}

	cmdBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("nep command marshal: %w", err)
	}

	cmdBytes = append(cmdBytes, '\n')

	_, err = conn.Write(cmdBytes)
	if err != nil {
		a.mu.Lock()
		conn.Close()
		a.conn = nil
		a.mu.Unlock()
		return "", fmt.Errorf("nep command write: %w", err)
	}

	a.logger.Printf("nep command: %s params=%v", command, params)
	return "ok", nil
}

// Status returns the current status of the NEP adapter.
func (a *NEPAdapter) Status(ctx context.Context) (DeviceStatus, error) {
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
func (a *NEPAdapter) IsConnected() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conn != nil
}

// Compile-time check that NEPAdapter implements DeviceControlAdapter.
var _ DeviceControlAdapter = (*NEPAdapter)(nil)

// NEPFacility represents an NEP facility discovered via API.
type NEPFacility struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Code     string `json:"code"`      // Facility code (e.g., "NEP-LA", "NEP-NY")
	City     string `json:"city"`
	Country  string `json:"country"`
	Timezone string `json:"timezone"`
	Status   string `json:"status"`    // "online", "offline", "maintenance"
	IP       string `json:"ip"`
	Port     int    `json:"port"`
	Devices  []string `json:"devices"`    // Device IDs at facility
}

// NEPRoute represents an NEP facility route.
type NEPRoute struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Protected   bool   `json:"protected"`
	Timestamp   string `json:"timestamp"`
	FacilityID  string `json:"facilityId"`
}

// NEPClient is a REST client for NEP facility control.
type NEPClient struct {
	baseURL string
	http    *http.Client
}

// NewNEPClient creates a new NEP REST client.
func NewNEPClient(baseURL string) *NEPClient {
	return &NEPClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// ListFacilities queries GET /nep/v1/facilities from the NEP registry.
func (c *NEPClient) ListFacilities(ctx context.Context) ([]NEPFacility, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/facilities", nil)
	if err != nil {
		return nil, fmt.Errorf("nep list facilities request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nep list facilities: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("nep list facilities %d", resp.StatusCode)
	}

	var facilities []NEPFacility
	if err := json.NewDecoder(resp.Body).Decode(&facilities); err != nil {
		return nil, fmt.Errorf("nep decode facilities: %w", err)
	}
	return facilities, nil
}

// GetFacility queries GET /nep/v1/facilities/{id}.
func (c *NEPClient) GetFacility(ctx context.Context, facilityID string) (*NEPFacility, error) {
	url := fmt.Sprintf("%s/facilities/%s", c.baseURL, facilityID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("nep get facility request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nep get facility: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("nep get facility %d", resp.StatusCode)
	}

	var facility NEPFacility
	if err := json.NewDecoder(resp.Body).Decode(&facility); err != nil {
		return nil, fmt.Errorf("nep decode facility: %w", err)
	}
	return &facility, nil
}

// GetRoute queries GET /nep/v1/facilities/{id}/route.
func (c *NEPClient) GetRoute(ctx context.Context, facilityID string) ([]NEPRoute, error) {
	url := fmt.Sprintf("%s/facilities/%s/route", c.baseURL, facilityID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("nep route request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nep route: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("nep route %d", resp.StatusCode)
	}

	var routes []NEPRoute
	if err := json.NewDecoder(resp.Body).Decode(&routes); err != nil {
		return nil, fmt.Errorf("nep decode route: %w", err)
	}
	return routes, nil
}

// SetRoute sets a facility route connection.
func (c *NEPClient) SetRoute(ctx context.Context, facilityID, source, destination string) error {
	url := fmt.Sprintf("%s/facilities/%s/route", c.baseURL, facilityID)

	payload := map[string]interface{}{
		"source":      source,
		"destination": destination,
		"action":      "take",
		"timestamp":   time.Now().Format(time.RFC3339),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("nep route marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("nep route request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("nep route set: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("nep route set %d", resp.StatusCode)
	}
	return nil
}

// SendCommand sends a device command via POST /nep/v1/facilities/{id}/command.
func (c *NEPClient) SendCommand(ctx context.Context, facilityID, deviceID, command string, params map[string]string) error {
	url := fmt.Sprintf("%s/facilities/%s/command", c.baseURL, facilityID)

	payload := map[string]interface{}{
		"deviceId":  deviceID,
		"command":   command,
		"params":    params,
		"timestamp": time.Now().Format(time.RFC3339),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("nep command marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("nep command request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("nep command send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("nep command send %d", resp.StatusCode)
	}
	return nil
}
