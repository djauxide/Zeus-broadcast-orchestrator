package routing

import (
	"context"
	"fmt"
	"log"
	"net"
	"strconv"
	"sync"
	"time"
)

// RouterAdapter is the interface for routing control protocol adapters.
// Adapters control broadcast routing routers (switchers) via vendor-specific
// control protocols (SW-P-08, Ember+, etc.).
type RouterAdapter interface {
	// Connect establishes a connection to the router.
	Connect(ctx context.Context) error

	// Disconnect closes the connection to the router.
	Disconnect(ctx context.Context) error

	// Route establishes a crosspoint connection from source to destination.
	Route(ctx context.Context, source, destination string) error

	// Unroute removes a crosspoint connection.
	Unroute(ctx context.Context, source, destination string) error

	// Status returns the current status of the router adapter.
	Status(ctx context.Context) (AdapterStatus, error)
}

// AdapterStatus describes the current state of a router adapter.
type AdapterStatus struct {
	Connected bool   `json:"connected"`
	Online    bool   `json:"online"`
	Router    string `json:"router,omitempty"`
	Error     string `json:"error,omitempty"`
}

// RouterConfig defines the configuration for connecting to a router.
type RouterConfig struct {
	// Protocol is the control protocol (swp08, emberplus).
	Protocol string `json:"protocol"`

	// Host is the router hostname or IP address.
	Host string `json:"host"`

	// Port is the router port.
	Port int `json:"port"`

	// RouterID is the router ID on the control network.
	RouterID string `json:"routerID,omitempty"`

	// FacilityID is the facility ID (for larger facilities).
	FacilityID string `json:"facilityID,omitempty"`
}

// RouterAdapterFactory creates a RouterAdapter from a config.
type RouterAdapterFactory func(config RouterConfig, logger *log.Logger) RouterAdapter

// RouterAdapterRegistry manages registered router adapter factories.
type RouterAdapterRegistry struct {
	adapters map[string]RouterAdapterFactory
}

// NewRouterAdapterRegistry creates a new empty adapter registry.
func NewRouterAdapterRegistry() *RouterAdapterRegistry {
	return &RouterAdapterRegistry{
		adapters: make(map[string]RouterAdapterFactory),
	}
}

// Register registers an adapter factory for a router type.
func (r *RouterAdapterRegistry) Register(deviceType string, factory RouterAdapterFactory) {
	r.adapters[deviceType] = factory
}

// Get returns the adapter factory for a device type, or an error if not registered.
func (r *RouterAdapterRegistry) Get(deviceType string) (RouterAdapterFactory, error) {
	factory, ok := r.adapters[deviceType]
	if !ok {
		return nil, &ErrAdapterNotFound{DeviceType: deviceType}
	}
	return factory, nil
}

// GetAdapter is an alias for Get to satisfy controller interfaces.
func (r *RouterAdapterRegistry) GetAdapter(deviceType string) (RouterAdapterFactory, error) {
	return r.Get(deviceType)
}

// RegisterRouterAdapter registers a router adapter factory in the global registry.
func RegisterRouterAdapter(deviceType string, factory RouterAdapterFactory) {
	GlobalRouterRegistry.Register(deviceType, factory)
}

// GetRouterAdapter returns a registered adapter factory by device type.
func GetRouterAdapter(deviceType string) (RouterAdapterFactory, error) {
	return GlobalRouterRegistry.Get(deviceType)
}

// GlobalRouterRegistry is the package-level adapter registry instance.
var GlobalRouterRegistry = NewRouterAdapterRegistry()

// ErrAdapterNotFound is returned when no adapter is found for a device type.
type ErrAdapterNotFound struct {
	DeviceType string
}

func (e *ErrAdapterNotFound) Error() string {
	return fmt.Sprintf("no routing adapter registered for device type: %s", e.DeviceType)
}

// SWP08Adapter implements DeviceControlAdapter for SW-P-08 router control.
// SW-P-08 is a serial/IP protocol used by Grass Valley routers for router
// control. Commands are sent as text commands over TCP/IP.
type SWP08Adapter struct {
	mu      sync.Mutex
	conn    net.Conn
	config  RouterConfig
	addr    string
	timeout time.Duration
	logger  *log.Logger
}

// NewSWP08Adapter creates a new SW-P-08 router control adapter.
func NewSWP08Adapter(config RouterConfig, logger *log.Logger) *SWP08Adapter {
	return &SWP08Adapter{
		config:  config,
		addr:    net.JoinHostPort(config.Host, strconv.Itoa(config.Port)),
		timeout: 5 * time.Second,
		logger:  logger,
	}
}

// Connect establishes a TCP connection to the SW-P-08 router.
func (a *SWP08Adapter) Connect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.conn != nil {
		return nil // already connected
	}

	d := net.Dialer{Timeout: a.timeout}
	conn, err := d.DialContext(ctx, "tcp", a.addr)
	if err != nil {
		return fmt.Errorf("swp08 connect: %w", err)
	}

	a.conn = conn
	a.logger.Printf("SW-P-08 connected to %s", a.addr)
	return nil
}

// Disconnect closes the connection to the SW-P-08 router.
func (a *SWP08Adapter) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.conn != nil {
		err := a.conn.Close()
		a.conn = nil
		if err != nil {
			return fmt.Errorf("swp08 disconnect: %w", err)
		}
		a.logger.Println("SW-P-08 disconnected")
	}
	return nil
}

// Route establishes a crosspoint connection from source to destination.
func (a *SWP08Adapter) Route(ctx context.Context, source, destination string) error {
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()

	if conn == nil {
		return fmt.Errorf("swp08: not connected")
	}

	// SW-P-08 command format: <STX>source,dest<CR><ETX>
	cmd := fmt.Sprintf("\x02%s,%s\r\x03", source, destination)
	_, err := conn.Write([]byte(cmd))
	if err != nil {
		a.mu.Lock()
		conn.Close()
		a.conn = nil
		a.mu.Unlock()
		return fmt.Errorf("swp08 route write: %w", err)
	}

	a.logger.Printf("SW-P-08 route: source=%s dest=%s", source, destination)
	return nil
}

// Unroute removes a crosspoint connection.
func (a *SWP08Adapter) Unroute(ctx context.Context, source, destination string) error {
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()

	if conn == nil {
		return fmt.Errorf("swp08: not connected")
	}

	// SW-P-08 unroute command: <STX>source,000<CR><ETX> (set destination to 000 to unroute)
	cmd := fmt.Sprintf("\x02%s,%s\r\x03", source, "000")
	_, err := conn.Write([]byte(cmd))
	if err != nil {
		a.mu.Lock()
		conn.Close()
		a.conn = nil
		a.mu.Unlock()
		return fmt.Errorf("swp08 unroute write: %w", err)
	}

	a.logger.Printf("SW-P-08 unroute: source=%s dest=%s", source, destination)
	return nil
}

// Status returns the current status of the SW-P-08 adapter.
func (a *SWP08Adapter) Status(ctx context.Context) (AdapterStatus, error) {
	a.mu.Lock()
	connected := a.conn != nil
	a.mu.Unlock()

	return AdapterStatus{
		Connected: connected,
		Online:    connected,
		Router:    a.addr,
	}, nil
}

// IsConnected returns whether the adapter is currently connected.
func (a *SWP08Adapter) IsConnected() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conn != nil
}

// Compile-time check that SWP08Adapter implements RouterAdapter.
var _ RouterAdapter = (*SWP08Adapter)(nil)

// Ensure SWP08Adapter is registered by default.
func init() {
	RegisterRouterAdapter("swp08", func(config RouterConfig, logger *log.Logger) RouterAdapter {
		return NewSWP08Adapter(config, logger)
	})
}

// EmberPlusAdapter implements DeviceControlAdapter for Ember+ control.
// Ember+ is a tree-based control protocol used by Lawo and other broadcast
// equipment manufacturers. This is a scaffold implementation.
type EmberPlusAdapter struct {
	mu      sync.Mutex
	conn    net.Conn
	config  RouterConfig
	addr    string
	timeout time.Duration
	logger  *log.Logger
}

// NewEmberPlusAdapter creates a new Ember+ router control adapter.
func NewEmberPlusAdapter(config RouterConfig, logger *log.Logger) *EmberPlusAdapter {
	return &EmberPlusAdapter{
		config:  config,
		addr:    net.JoinHostPort(config.Host, strconv.Itoa(config.Port)),
		timeout: 5 * time.Second,
		logger:  logger,
	}
}

// Connect establishes a TCP connection to the Ember+ device.
func (a *EmberPlusAdapter) Connect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.conn != nil {
		return nil // already connected
	}

	d := net.Dialer{Timeout: a.timeout}
	conn, err := d.DialContext(ctx, "tcp", a.addr)
	if err != nil {
		return fmt.Errorf("emberplus connect: %w", err)
	}

	a.conn = conn
	a.logger.Printf("Ember+ connected to %s", a.addr)
	return nil
}

// Disconnect closes the connection to the Ember+ device.
func (a *EmberPlusAdapter) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.conn != nil {
		err := a.conn.Close()
		a.conn = nil
		if err != nil {
			return fmt.Errorf("emberplus disconnect: %w", err)
		}
		a.logger.Println("Ember+ disconnected")
	}
	return nil
}

// Route establishes a crosspoint connection from source to destination.
func (a *EmberPlusAdapter) Route(ctx context.Context, source, destination string) error {
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()

	if conn == nil {
		return fmt.Errorf("emberplus: not connected")
	}

	// Ember+ uses JSON-RPC over TCP for commands.
	cmd := fmt.Sprintf("{\"method\":\"connect\",\"source\":\"%s\",\"destination\":\"%s\"}\n", source, destination)
	_, err := conn.Write([]byte(cmd))
	if err != nil {
		a.mu.Lock()
		conn.Close()
		a.conn = nil
		a.mu.Unlock()
		return fmt.Errorf("emberplus route write: %w", err)
	}

	a.logger.Printf("Ember+ route: source=%s dest=%s", source, destination)
	return nil
}

// Unroute removes a crosspoint connection.
func (a *EmberPlusAdapter) Unroute(ctx context.Context, source, destination string) error {
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()

	if conn == nil {
		return fmt.Errorf("emberplus: not connected")
	}

	cmd := fmt.Sprintf("{\"method\":\"disconnect\",\"source\":\"%s\",\"destination\":\"%s\"}\n", source, destination)
	_, err := conn.Write([]byte(cmd))
	if err != nil {
		a.mu.Lock()
		conn.Close()
		a.conn = nil
		a.mu.Unlock()
		return fmt.Errorf("emberplus unroute write: %w", err)
	}

	a.logger.Printf("Ember+ unroute: source=%s dest=%s", source, destination)
	return nil
}

// Status returns the current status of the Ember+ adapter.
func (a *EmberPlusAdapter) Status(ctx context.Context) (AdapterStatus, error) {
	a.mu.Lock()
	connected := a.conn != nil
	a.mu.Unlock()

	return AdapterStatus{
		Connected: connected,
		Online:    connected,
		Router:    a.addr,
	}, nil
}

// IsConnected returns whether the adapter is currently connected.
func (a *EmberPlusAdapter) IsConnected() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conn != nil
}

// Compile-time check that EmberPlusAdapter implements RouterAdapter.
var _ RouterAdapter = (*EmberPlusAdapter)(nil)

// Ensure EmberPlus adapter is registered by default.
func init() {
	RegisterRouterAdapter("emberplus", func(config RouterConfig, logger *log.Logger) RouterAdapter {
		return NewEmberPlusAdapter(config, logger)
	})
}
