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

// CerebrumAdapter controls EVS Cerebrum, the orchestration and asset
// management layer for EVS live replay servers (XT3, XT-VIA, XS-NEO).
// Cerebrum manages media assets, automates playout, and orchestrates
// clips and replays across multiple EVS servers in live production.
//
// This adapter connects to the Cerebrum REST API to load assets, control
// playout (play/stop/record), query playlist status, and monitor device
// health for live sports and event production.
type CerebrumAdapter struct {
	mu      sync.Mutex
	conn    net.Conn
	config  DeviceConfig
	addr    string
	timeout time.Duration
	logger  *log.Logger

	httpClient *http.Client
	baseURL    string
	token      string
}

// Compile-time check that CerebrumAdapter implements DeviceControlAdapter.
var _ DeviceControlAdapter = (*CerebrumAdapter)(nil)

func init() {
	RegisterDeviceAdapter("cerebrum", func(config DeviceConfig, logger *log.Logger) DeviceControlAdapter {
		return NewCerebrumAdapter(config, logger)
	})
	RegisterDeviceAdapter("evs", func(config DeviceConfig, logger *log.Logger) DeviceControlAdapter {
		return NewCerebrumAdapter(config, logger)
	})
	RegisterDeviceAdapter("evs-cerebrum", func(config DeviceConfig, logger *log.Logger) DeviceControlAdapter {
		return NewCerebrumAdapter(config, logger)
	})
}

// NewCerebrumAdapter creates a new EVS Cerebrum device control adapter.
func NewCerebrumAdapter(config DeviceConfig, logger *log.Logger) *CerebrumAdapter {
	addr := net.JoinHostPort(config.Host, strconv.Itoa(config.Port))
	if config.Port == 0 {
		addr = config.Host
	}
	baseURL := config.Host
	if baseURL == "" {
		baseURL = "http://" + addr
	}
	return &CerebrumAdapter{
		config:     config,
		addr:       addr,
		timeout:    5 * time.Second,
		logger:     logger,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		baseURL:    baseURL,
	}
}

// Connect validates reachability of the Cerebrum REST endpoint.
func (a *CerebrumAdapter) Connect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	url := a.baseURL + "/api/v1/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("cerebrum connect request: %w", err)
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cerebrum connect: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("cerebrum connect status %d: %s", resp.StatusCode, body)
	}
	a.logger.Printf("EVS Cerebrum connected to %s", a.baseURL)
	return nil
}

// Disconnect clears the Cerebrum session and token.
func (a *CerebrumAdapter) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.token = ""
	a.logger.Printf("EVS Cerebrum disconnected from %s", a.baseURL)
	return nil
}

// SendCommand sends a Cerebrum control command. Supported commands:
//   - "load-asset"   {assetId: "...", channel: "..."}
//   - "play"         {channel: "..."}
//   - "stop"         {channel: "..."}
//   - "record"       {channel: "...", duration: "..."}
//   - "playlist"     {channel: "..."}
//   - "get-status"   {channel: "..."}
func (a *CerebrumAdapter) SendCommand(ctx context.Context, command string, params map[string]string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	payload := map[string]interface{}{}
	for k, v := range params {
		payload[k] = v
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("cerebrum marshal command %s: %w", command, err)
	}

	url := a.baseURL + "/api/v1/command/" + command
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytesReader(body))
	if err != nil {
		return "", fmt.Errorf("cerebrum command request %s: %w", command, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("cerebrum command %s: %w", command, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("cerebrum read response %s: %w", command, err)
	}
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("cerebrum command %s status %d: %s", command, resp.StatusCode, respBody)
	}

	a.logger.Printf("EVS Cerebrum command %s params=%v", command, params)
	return string(respBody), nil
}

// Status returns the current Cerebrum device status.
func (a *CerebrumAdapter) Status(ctx context.Context) (DeviceStatus, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	url := a.baseURL + "/api/v1/status"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return DeviceStatus{}, fmt.Errorf("cerebrum status request: %w", err)
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
		Device:    "cerebrum:" + a.config.Host,
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
