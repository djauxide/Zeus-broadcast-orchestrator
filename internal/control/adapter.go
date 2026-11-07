package control

import (
	"context"
	"fmt"
)

// Adapter is the interface for routing control protocol adapters.
// Adapters translate Zeus routing intent into vendor-specific router
// control protocols (SW-P-08, Ember+, etc.).
type Adapter interface {
	// Connect establishes a connection to the router.
	Connect(ctx context.Context) error

	// Disconnect closes the connection to the router.
	Disconnect(ctx context.Context) error

	// Route establishes a crosspoint connection from source to destination
	// on the router.
	Route(ctx context.Context, source, destination string) error

	// Unroute removes a crosspoint connection.
	Unroute(ctx context.Context, source, destination string) error

	// Status returns the current status of the adapter and router.
	Status(ctx context.Context) (AdapterStatus, error)
}

// AdapterStatus describes the current state of a control adapter.
type AdapterStatus struct {
	Connected bool   `json:"connected"`
	Router    string `json:"router,omitempty"`
	Error     string `json:"error,omitempty"`
}

// RouterConfig defines the configuration for connecting to a router.
type RouterConfig struct {
	// Protocol is the routing protocol (swp08, emberplus).
	Protocol string `json:"protocol"`

	// Host is the router hostname or IP address.
	Host string `json:"host"`

	// Port is the router port.
	Port int `json:"port"`

	// SourceID is the source ID on the router for this NAP.
	SourceID string `json:"sourceID"`

	// DestinationID is the destination ID on the router for this NAP.
	DestinationID string `json:"destinationID"`
}

// ErrAdapterNotFound is returned when no adapter is registered for a protocol.
type ErrAdapterNotFound struct {
	Protocol string
}

func (e *ErrAdapterNotFound) Error() string {
	return fmt.Sprintf("no routing adapter registered for protocol: %s", e.Protocol)
}

// AdapterRegistry manages registered routing adapters.
type AdapterRegistry struct {
	adapters map[string]Adapter
}

// NewAdapterRegistry creates a new empty adapter registry.
func NewAdapterRegistry() *AdapterRegistry {
	return &AdapterRegistry{
		adapters: make(map[string]Adapter),
	}
}

// Register registers an adapter for a protocol.
func (r *AdapterRegistry) Register(protocol string, adapter Adapter) {
	r.adapters[protocol] = adapter
}

// Get returns the adapter for a protocol, or an error if not registered.
func (r *AdapterRegistry) Get(protocol string) (Adapter, error) {
	adapter, ok := r.adapters[protocol]
	if !ok {
		return nil, &ErrAdapterNotFound{Protocol: protocol}
	}
	return adapter, nil
}

// GetAdapter returns the adapter for a protocol. It is an alias for Get
// to satisfy the controller.RoutingClient interface.
func (r *AdapterRegistry) GetAdapter(protocol string) (Adapter, error) {
	return r.Get(protocol)
}

// List returns all registered protocol names.
func (r *AdapterRegistry) List() []string {
	protocols := make([]string, 0, len(r.adapters))
	for p := range r.adapters {
		protocols = append(protocols, p)
	}
	return protocols
}
