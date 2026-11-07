package hitless

import (
	"context"
	"sync"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
	"github.com/zeus-broadcast/zeus-kernel/internal/transport/rist"
	"github.com/zeus-broadcast/zeus-kernel/internal/transport/srt"
)

// Merger combines multiple WAN contribution paths for hitless merge. It
// manages one bridge per path and aggregates their health into a single
// PathHealth view. When all paths are up, the merged output is considered
// healthy; when any path is down, the merge is degraded but still operational
// (the surviving path carries the stream).
type Merger struct {
	mu     sync.Mutex
	bridges map[string]pathBridge
	health  map[string]zeusv1.PathHealth
}

// pathBridge pairs a started bridge with its transport type.
type pathBridge struct {
	transport string
	bridge    interface {
		Health(context.Context) (zeusv1.PathHealth, error)
		Stop(context.Context) error
	}
}

// NewMerger creates a hitless merger with no paths.
func NewMerger() *Merger {
	return &Merger{
		bridges: make(map[string]pathBridge),
		health:  make(map[string]zeusv1.PathHealth),
	}
}

// AddSRTPath registers an SRT path with the merger. The bridge is started
// immediately with the given spec and passphrase.
func (m *Merger) AddSRTPath(ctx context.Context, name string, spec zeusv1.SRTSpec, passphrase []byte) error {
	b := srt.NewBridge()
	if err := b.Start(ctx, spec, passphrase); err != nil {
		return err
	}
	m.mu.Lock()
	m.bridges[name] = pathBridge{transport: "srt", bridge: startedBridge{b: b}}
	m.health[name] = zeusv1.PathHealth{Name: name, Transport: "srt", State: "down"}
	m.mu.Unlock()
	return nil
}

// AddRISTPath registers a RIST path with the merger. The bridge is started
// immediately with the given spec and passphrase.
func (m *Merger) AddRISTPath(ctx context.Context, name string, spec zeusv1.RISTSpec, passphrase []byte) error {
	b := rist.NewBridge()
	if err := b.Start(ctx, spec, passphrase); err != nil {
		return err
	}
	m.mu.Lock()
	m.bridges[name] = pathBridge{transport: "rist", bridge: startedBridge{b: b}}
	m.health[name] = zeusv1.PathHealth{Name: name, Transport: "rist", State: "down"}
	m.mu.Unlock()
	return nil
}

// Health returns the aggregated health of all paths. The merged state is "up"
// if all paths are up, "degraded" if at least one path is up, and "down" if
// all paths are down.
func (m *Merger) Health(ctx context.Context) (zeusv1.PathHealth, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Collect all known path names (union of bridges and health map keys).
	allNames := make(map[string]bool)
	for name := range m.bridges {
		allNames[name] = true
	}
	for name := range m.health {
		allNames[name] = true
	}

	var paths []zeusv1.PathHealth
	upCount := 0
	degradedCount := 0
	downCount := 0

	for _, name := range sortedPathNamesFromSet(allNames) {
		ph, ok := m.health[name]
		if !ok {
			ph = zeusv1.PathHealth{Name: name, State: "down"}
		}
		paths = append(paths, ph)
		switch ph.State {
		case "up":
			upCount++
		case "degraded":
			degradedCount++
		default:
			downCount++
		}
	}

	merged := zeusv1.PathHealth{
		Name:      "merged",
		Transport: "hitless",
		State:     "down",
	}
	totalPaths := len(paths)
	switch {
	case totalPaths > 0 && upCount == totalPaths:
		merged.State = "up"
	case upCount > 0 || degradedCount > 0:
		merged.State = "degraded"
	default:
		merged.State = "down"
	}

	// Aggregate RTT as the max across paths (worst case).
	maxRTT := int32(0)
	for _, ph := range paths {
		if ph.RTTMs > maxRTT {
			maxRTT = ph.RTTMs
		}
	}
	merged.RTTMs = maxRTT

	return merged, nil
}

// MeasureHealth runs Health() on all bridges and updates the merger's health map.
func (m *Merger) MeasureHealth(ctx context.Context) error {
	m.mu.Lock()
	names := make([]string, 0, len(m.bridges))
	for name := range m.bridges {
		names = append(names, name)
	}
	bridges := make(map[string]pathBridge, len(m.bridges))
	for k, v := range m.bridges {
		bridges[k] = v
	}
	m.mu.Unlock()

	for _, name := range names {
		pb := bridges[name]
		ph, err := pb.bridge.Health(ctx)
		if err != nil {
			ph = zeusv1.PathHealth{Name: name, State: "down"}
		}
		ph.Name = name
		ph.Transport = pb.transport
		m.UpdatePathHealth(name, ph)
	}
	return nil
}

// UpdatePathHealth records the measured health for a named path.
func (m *Merger) UpdatePathHealth(name string, ph zeusv1.PathHealth) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.health[name] = ph
}

// RemovePath stops and removes a path from the merger.
func (m *Merger) RemovePath(ctx context.Context, name string) error {
	m.mu.Lock()
	pb, ok := m.bridges[name]
	if !ok {
		m.mu.Unlock()
		return &UnknownPathError{name: name}
	}
	delete(m.bridges, name)
	delete(m.health, name)
	m.mu.Unlock()
	return pb.bridge.Stop(ctx)
}

// PathHealth returns the last measured health for a named path, and whether it exists.
func (m *Merger) PathHealth(name string) (zeusv1.PathHealth, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ph, ok := m.health[name]
	return ph, ok
}

// StopAll stops all registered bridges.
func (m *Merger) StopAll(ctx context.Context) error {
	m.mu.Lock()
	bridges := make(map[string]pathBridge, len(m.bridges))
	for k, v := range m.bridges {
		bridges[k] = v
	}
	m.bridges = make(map[string]pathBridge)
	m.health = make(map[string]zeusv1.PathHealth)
	m.mu.Unlock()

	var firstErr error
	for name, pb := range bridges {
		if err := pb.bridge.Stop(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
		_ = name
	}
	return firstErr
}

// sortedPathNames returns path names in deterministic order.
func sortedPathNames(bridges map[string]pathBridge) []string {
	names := make([]string, 0, len(bridges))
	for name := range bridges {
		names = append(names, name)
	}
	return sortedPathNamesFromSet(toSet(names))
}

// sortedPathNamesFromSet returns path names from a set in deterministic order.
func sortedPathNamesFromSet(names map[string]bool) []string {
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		key := keys[i]
		j := i - 1
		for j >= 0 && keys[j] > key {
			keys[j+1] = keys[j]
			j--
		}
		keys[j+1] = key
	}
	return keys
}

// toSet converts a slice to a set.
func toSet(names []string) map[string]bool {
	s := make(map[string]bool, len(names))
	for _, n := range names {
		s[n] = true
	}
	return s
}

// UnknownPathError is returned when a path name is not registered.
type UnknownPathError struct {
	name string
}

func (e *UnknownPathError) Error() string {
	return "hitless: unknown path " + e.name
}

// startedBridge wraps a bridge that has already been started, exposing only
// Health and Stop.
type startedBridge struct {
	b interface {
		Health(context.Context) (zeusv1.PathHealth, error)
		Stop(context.Context) error
	}
}

func (a startedBridge) Health(ctx context.Context) (zeusv1.PathHealth, error) {
	return a.b.Health(ctx)
}

func (a startedBridge) Stop(ctx context.Context) error {
	return a.b.Stop(ctx)
}
