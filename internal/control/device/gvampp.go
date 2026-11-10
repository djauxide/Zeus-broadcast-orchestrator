package device

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"log"
	"net"
	"strconv"
)

// GVAMPAdapter controls Grass Valley AMPP (Agile Media Processing Platform)
// devices via the AMPP REST API and iDCP-over-HTTP. AMPP is a software-defined,
// IP-native production platform running on COTS hardware with NMOS IS-04/IS-05
// discovery and ST 2110 media.
//
// This adapter discovers AMPP nodes via NMOS IS-04 and issues control commands
// via the AMPP device API (start/stop channels, set sources, query status).
type GVAMPAdapter struct {
	mu      sync.Mutex
	conn    net.Conn
	config  DeviceConfig
	addr    string
	timeout time.Duration
	logger  *log.Logger

	httpClient *http.Client
	baseURL    string
	token      string
	tokenExpiry time.Time
}

// Compile-time check that GVAMPAdapter implements DeviceControlAdapter.
var _ DeviceControlAdapter = (*GVAMPAdapter)(nil)

func init() {
	RegisterDeviceAdapter("gvampp", func(config DeviceConfig, logger *log.Logger) DeviceControlAdapter {
		return NewGVAMPAdapter(config, logger)
	})
	RegisterDeviceAdapter("ampp", func(config DeviceConfig, logger *log.Logger) DeviceControlAdapter {
		return NewGVAMPAdapter(config, logger)
	})
}

// NewGVAMPAdapter creates a new GV AMPP device control adapter.
func NewGVAMPAdapter(config DeviceConfig, logger *log.Logger) *GVAMPAdapter {
	addr := net.JoinHostPort(config.Host, strconv.Itoa(config.Port))
	if config.Port == 0 {
		addr = config.Host // may be a full URL
	}
	baseURL := config.Host
	if baseURL == "" {
		baseURL = "http://" + addr
	}
	return &GVAMPAdapter{
		config:     config,
		addr:       addr,
		timeout:    5 * time.Second,
		logger:     logger,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		baseURL:    baseURL,
	}
}

// Connect establishes a session with the AMPP device. AMPP is HTTP-based so
// this validates the endpoint is reachable and optionally acquires a token.
func (a *GVAMPAdapter) Connect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// AMPP control endpoint: GET /api/v1/health or /api/v1/channels
	url := a.baseURL + "/api/v1/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("gvampp connect request: %w", err)
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("gvampp connect: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gvampp connect status %d: %s", resp.StatusCode, body)
	}
	a.logger.Printf("GV AMPP connected to %s", a.baseURL)
	return nil
}

// Disconnect closes the AMPP session (clears token).
func (a *GVAMPAdapter) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.token = ""
	a.logger.Printf("GV AMPP disconnected from %s", a.baseURL)
	return nil
}

// SendCommand sends an AMPP control command. Supported commands:
//   - "start-channel" {channel: "1", source: "..."}
//   - "stop-channel"  {channel: "1"}
//   - "set-source"    {channel: "1", source: "..."}
//   - "get-status"    {channel: "1"}
func (a *GVAMPAdapter) SendCommand(ctx context.Context, command string, params map[string]string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	var payload map[string]interface{}
	switch command {
	case "start-channel", "stop-channel", "set-source":
		payload = map[string]interface{}{
			"channel": params["channel"],
			"source":  params["source"],
		}
	case "get-status":
		payload = map[string]interface{}{
			"channel": params["channel"],
		}
	default:
		payload = params
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("gvampp marshal command %s: %w", command, err)
	}

	url := a.baseURL + "/api/v1/command/" + command
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", fmt.Errorf("gvampp command request %s: %w", command, err)
	}
	req.Body = bytesReader(body)
	req.Header.Set("Content-Type", "application/json")
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("gvampp command %s: %w", command, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("gvampp read response %s: %w", command, err)
	}
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("gvampp command %s status %d: %s", command, resp.StatusCode, respBody)
	}

	a.logger.Printf("GV AMPP command %s channel=%s source=%s", command, params["channel"], params["source"])
	return string(respBody), nil
}

// Status returns the current AMPP device status.
func (a *GVAMPAdapter) Status(ctx context.Context) (DeviceStatus, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	url := a.baseURL + "/api/v1/status"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return DeviceStatus{}, fmt.Errorf("gvampp status request: %w", err)
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
		Device:    "gvampp:" + a.config.Host,
	}, nil
}

// bytesReader wraps a []byte as an io.ReadSeeker for http request bodies.
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

func (br *bytesReader) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		br.i = int(offset)
	case io.SeekCurrent:
		br.i += int(offset)
	case io.SeekEnd:
		br.i = len(br.b) + int(offset)
	}
	return int64(br.i), nil
}

func (br *bytesReader) Close() error { return nil }
