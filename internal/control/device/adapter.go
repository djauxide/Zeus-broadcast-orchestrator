package device

import (
	"context"
	"fmt"
)

// DeviceControlAdapter is the interface for device control adapters.
// Adapters control broadcast devices (cameras, audio devices, replay
// servers, graphics systems) via vendor-specific control protocols.
type DeviceControlAdapter interface {
	// Connect establishes a connection to the device.
	Connect(ctx context.Context) error

	// Disconnect closes the connection to the device.
	Disconnect(ctx context.Context) error

	// SendCommand sends a control command to the device.
	SendCommand(ctx context.Context, command string, params map[string]string) (string, error)

	// Status returns the current status of the device.
	Status(ctx context.Context) (DeviceStatus, error)
}

// DeviceStatus describes the current state of a controlled device.
type DeviceStatus struct {
	Connected bool   `json:"connected"`
	Online    bool   `json:"online"`
	Device    string `json:"device,omitempty"`
	Error     string `json:"error,omitempty"`
}

// DeviceConfig defines the configuration for connecting to a device.
type DeviceConfig struct {
	// Type is the device type (camera, audio, replay, graphics).
	Type string `json:"type"`

	// Protocol is the control protocol (visca, emberplus, etc.).
	Protocol string `json:"protocol"`

	// Host is the device hostname or IP address.
	Host string `json:"host"`

	// Port is the device port.
	Port int `json:"port"`

	// DeviceID is the device ID on the control network.
	DeviceID string `json:"deviceID,omitempty"`
}

// ErrDeviceNotFound is returned when no adapter is found for a device type.
type ErrDeviceNotFound struct {
	DeviceType string
}

func (e *ErrDeviceNotFound) Error() string {
	return fmt.Sprintf("no device control adapter registered for device type: %s", e.DeviceType)
}

// AdapterRegistry manages registered device control adapters.
type AdapterRegistry struct {
	adapters map[string]DeviceControlAdapter
}

// NewAdapterRegistry creates a new empty adapter registry.
func NewAdapterRegistry() *AdapterRegistry {
	return &AdapterRegistry{
		adapters: make(map[string]DeviceControlAdapter),
	}
}

// Register registers an adapter for a device type.
func (r *AdapterRegistry) Register(deviceType string, adapter DeviceControlAdapter) {
	r.adapters[deviceType] = adapter
}

// Get returns the adapter for a device type, or an error if not registered.
func (r *AdapterRegistry) Get(deviceType string) (DeviceControlAdapter, error) {
	adapter, ok := r.adapters[deviceType]
	if !ok {
		return nil, &ErrDeviceNotFound{DeviceType: deviceType}
	}
	return adapter, nil
}

// GetAdapter is an alias for Get to satisfy controller interfaces.
func (r *AdapterRegistry) GetAdapter(deviceType string) (DeviceControlAdapter, error) {
	return r.Get(deviceType)
}

// List returns all registered device type names.
func (r *AdapterRegistry) List() []string {
	types := make([]string, 0, len(r.adapters))
	for t := range r.adapters {
		types = append(types, t)
	}
	return types
}
