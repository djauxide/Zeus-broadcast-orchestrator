package device

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"log"
)

// NEPPAdapter controls NEP Group devices (outside-broadcast facilities,
// mobile units, flyaway packages) via the NEP TFC REST API. NEP TFC is
// the technical facility control layer for NEP's global production fleet.
//
// This adapter connects to NEP device endpoints over TCP/REST, supporting
// facility-level control: route take, device power, tally query, and
// status monitoring for NEP OB trucks and flyaways.
type NEPPAdapter struct {
	mu      sync.Mutex
	conn    net.Conn
	config  DeviceConfig
	addr    string
	timeout time.Duration
	logger  *log.Logger

	httpClient *http.Client
	baseURL    string
}

// Compile-time check that NEPPAdapter implements DeviceControlAdapter.
var _ DeviceControlAdapter = (*NEPPAdapter)(nil)

func init() {
	RegisterDeviceAdapter("nep", func(config DeviceConfig, logger *log.Logger) DeviceControlAdapter {
		return NewNEPPAdapter(config, logger)
	})
	RegisterDeviceAdapter("nep-tfc", func(config DeviceConfig, logger *log.Logger) DeviceControlAdapter {
		return NewNEPPAdapter(config, logger)
	})
	RegisterDeviceAdapter("tfc", func(config DeviceConfig, logger *log.Logger) DeviceControlAdapter {
		return NewNEPPAdapter(config, logger)
	})
}

// NewNEPPAdapter creates a new NEP TFC device control adapter.
func NewNEPPAdapter(config DeviceConfig, logger *log.Logger) *NEPPAdapter {
	addr := net.JoinHostPort(config.Host, strconv.Itoa(config.Port))
	if config.Port == 0 {
		addr = config.Host
	}
	baseURL := config.Host
	if baseURL == "" {
		baseURL = "http://" + addr
	}
	return &NEPPAdapter{
		config:     config,
		addr:       addr,
		timeout:    5 * time.Second,
		logger:     logger,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		baseURL:    baseURL,
	}
}

// Connect validates reachability of the NEP TFC endpoint.
func (a *NEPPAdapter) Connect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	url := a.baseURL + "/api/v1/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("nep connect request: %w", err)
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("nep connect: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("nep connect status %d: %s", resp.StatusCode, body)
	}
	a.logger.Printf("NEP TFC connected to %s", a.baseURL)
	return nil
}

// Disconnect clears the NEP session.
func (a *NEPPAdapter) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.logger.Printf("NEP TFC disconnected from %s", a.baseURL)
	return nil
}

// SendCommand sends a NEP TFC control command. Supported commands:
//   - "route-take"   {source: "...", destination: "..."}
//   - "device-power" {device: "...", power: "on|off"}
//   - "tally-query"  {destination: "..."}
//   - "get-status"   {device: "..."}
func (a *NEPPAdapter) SendCommand(ctx context.Context, command string, params map[string]string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	payload := map[string]interface{}{}
	for k, v := range params {
		payload[k] = v
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("nep marshal command %s: %w", command, err)
	}

	url := a.baseURL + "/api/v1/command/" + command
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytesReader(body))
	if err != nil {
		return "", fmt.Errorf("nep command request %s: %w", command, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("nep command %s: %w", command, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("nep read response %s: %w", command, err)
	}
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("nep command %s status %d: %s", command, resp.StatusCode, respBody)
	}

	a.logger.Printf("NEP TFC command %s params=%v", command, params)
	return string(respBody), nil
}

// Status returns the current NEP device status.
func (a *NEPPAdapter) Status(ctx context.Context) (DeviceStatus, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	url := a.baseURL + "/api/v1/status"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return DeviceStatus{}, fmt.Errorf("nep status request: %w", err)
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return DeviceStatus{Connected: false, Online: false, Error: err.Error()}, nil
	}
	defer resp.Body.Close()

	online := resp.StatusCode/100 == 2
	return DeviceStatus{
		Connected: online,
		Online:    online,
		Device:    "nep:" + a.config.Host,
	}, nil
}

// bytesReader wraps a []byte as an io.ReadCloser for http request bodies.
type bytesReader struct {
	b []byte
	i int
}

func (br *bytesReader) Read(p []byte) (int, error) {
	if br.i >= len(br.b) {
		return 0, io.EOF
	}
	n := copy(p, br.b[br.i:])
	br.i += n
	return n, nil
}

func (br *bytesReader) Close() error { return nil }

var _ = fmt.Sprintf
