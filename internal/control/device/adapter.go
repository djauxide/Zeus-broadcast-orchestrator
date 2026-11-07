package device

import (
	"context"
	"fmt"
	"log"
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

	// FacilityID is the facility ID (for NEP, AMPP, etc.).
	FacilityID string `json:"facilityID,omitempty"`
}

// ErrDeviceNotFound is returned when no adapter is found for a device type.
type ErrDeviceNotFound struct {
	DeviceType string
}

func (e *ErrDeviceNotFound) Error() string {
	return fmt.Sprintf("no device control adapter registered for device type: %s", e.DeviceType)
}

// DeviceAdapterFactory creates a DeviceControlAdapter from a config.
type DeviceAdapterFactory func(config DeviceConfig, logger *log.Logger) DeviceControlAdapter

// AdapterRegistry manages registered device control adapter factories.
type AdapterRegistry struct {
	adapters map[string]DeviceAdapterFactory
}

// NewAdapterRegistry creates a new empty adapter registry.
func NewAdapterRegistry() *AdapterRegistry {
	return &AdapterRegistry{
		adapters: make(map[string]DeviceAdapterFactory),
	}
}

// Register registers an adapter factory for a device type.
func (r *AdapterRegistry) Register(deviceType string, factory DeviceAdapterFactory) {
	r.adapters[deviceType] = factory
}

// Get returns the adapter factory for a device type, or an error if not registered.
func (r *AdapterRegistry) Get(deviceType string) (DeviceAdapterFactory, error) {
	factory, ok := r.adapters[deviceType]
	if !ok {
		return nil, &ErrDeviceNotFound{DeviceType: deviceType}
	}
	return factory, nil
}

// GetAdapter is an alias for Get to satisfy controller interfaces.
func (r *AdapterRegistry) GetAdapter(deviceType string) (DeviceAdapterFactory, error) {
	return r.Get(deviceType)
}

// RegisterDeviceAdapter registers a device control adapter factory for a device type
// in the global registry.
func RegisterDeviceAdapter(deviceType string, factory DeviceAdapterFactory) {
	GlobalRegistry.Register(deviceType, factory)
}

// GetDeviceAdapter returns a registered adapter factory by device type.
func GetDeviceAdapter(deviceType string) (DeviceAdapterFactory, error) {
	return GlobalRegistry.Get(deviceType)
}

// GlobalRegistry is the package-level adapter registry instance.
var GlobalRegistry = NewAdapterRegistry()

// init registers built-in device adapters.
func init() {
	RegisterDeviceAdapter("camera", func(config DeviceConfig, logger *log.Logger) DeviceControlAdapter {
		return NewCameraAdapter(config, logger)
	})
	RegisterDeviceAdapter("audio", func(config DeviceConfig, logger *log.Logger) DeviceControlAdapter {
		return NewAudioAdapter(config, logger)
	})
	RegisterDeviceAdapter("replay", func(config DeviceConfig, logger *log.Logger) DeviceControlAdapter {
		return NewReplayAdapter(config, logger)
	})
	RegisterDeviceAdapter("graphics", func(config DeviceConfig, logger *log.Logger) DeviceControlAdapter {
		return NewGraphicsAdapter(config, logger)
	})
}
