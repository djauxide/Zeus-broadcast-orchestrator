package device

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
)

type RFAdapter struct {
	mu        sync.Mutex
	conn      net.Conn
	config    DeviceConfig
	addr      string
	timeout   time.Duration
	logger    *log.Logger
	baseURL   string
	httpClient *http.Client
}

func NewRFAdapter(config DeviceConfig, logger *log.Logger) *RFAdapter {
	return &RFAdapter{
		config:   config,
		addr:     net.JoinHostPort(config.Host, strconv.Itoa(config.Port)),
		timeout:  5 * time.Second,
		logger:   logger,
		baseURL:  fmt.Sprintf("http://%s:%d/rf/v1", config.Host, config.Port),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (a *RFAdapter) Connect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.conn != nil {
		return nil
	}
	d := net.Dialer{Timeout: a.timeout}
	conn, err := d.DialContext(ctx, "tcp", a.addr)
	if err != nil {
		return fmt.Errorf("RF connect: %w", err)
	}
	a.conn = conn
	a.logger.Printf("RF connected to %s", a.addr)
	return nil
}

func (a *RFAdapter) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.conn != nil {
		err := a.conn.Close()
		a.conn = nil
		if err != nil {
			return fmt.Errorf("RF disconnect: %w", err)
		}
		a.logger.Println("RF disconnected")
	}
	return nil
}

func (a *RFAdapter) SendCommand(ctx context.Context, command string, params map[string]string) (string, error) {
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()
	if conn == nil {
		return "", fmt.Errorf("RF: not connected")
	}
	cmd := fmt.Sprintf("%s %s\n", command, paramsToString(params))
	_, err := conn.Write([]byte(cmd))
	if err != nil {
		a.mu.Lock()
		conn.Close()
		a.conn = nil
		a.mu.Unlock()
		return "", fmt.Errorf("RF command write: %w", err)
	}
	a.logger.Printf("RF command: %s params=%v", command, params)
	return "ok", nil
}

func (a *RFAdapter) Status(ctx context.Context) (DeviceStatus, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return DeviceStatus{
		Connected: a.conn != nil,
		Online:    a.conn != nil,
		Device:    a.addr,
	}, nil
}

func (a *RFAdapter) IsConnected() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conn != nil
}

var _ DeviceControlAdapter = (*RFAdapter)(nil)

func init() {
	RegisterDeviceAdapter("rf", func(config DeviceConfig, logger *log.Logger) DeviceControlAdapter {
		return NewRFAdapter(config, logger)
	})
}

func paramsToString(params map[string]string) string {
	if len(params) == 0 {
		return ""
	}
	result := ""
	for k, v := range params {
		result += fmt.Sprintf("%s=%s ", k, v)
	}
	return result[:len(result)-1]
}