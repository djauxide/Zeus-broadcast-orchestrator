package hitless

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
)

// fakeBridge implements a test double for the bridge interface used by Merger.
type fakeBridge struct {
	ph zeusv1.PathHealth
	err error
}

func (f *fakeBridge) Health(context.Context) (zeusv1.PathHealth, error) {
	return f.ph, f.err
}
func (f *fakeBridge) Stop(context.Context) error { return nil }

// TestMergerHealthAllUp verifies that when all paths are up, merged state is "up".
func TestMergerHealthAllUp(t *testing.T) {
	m := NewMerger()
	ctx := context.Background()

	m.UpdatePathHealth("primary", zeusv1.PathHealth{Name: "primary", Transport: "srt", State: "up", RTTMs: 100})
	m.UpdatePathHealth("backup", zeusv1.PathHealth{Name: "backup", Transport: "rist", State: "up", RTTMs: 150})

	merged, err := m.Health(ctx)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if merged.State != "up" {
		t.Fatalf("merged state = %q, want up", merged.State)
	}
	if merged.RTTMs != 150 {
		t.Fatalf("merged rttMs = %d, want 150 (max)", merged.RTTMs)
	}
}

// TestMergerHealthOneDown verifies degraded state when one path is down.
func TestMergerHealthOneDown(t *testing.T) {
	m := NewMerger()
	ctx := context.Background()

	m.UpdatePathHealth("primary", zeusv1.PathHealth{Name: "primary", Transport: "srt", State: "up", RTTMs: 100})
	m.UpdatePathHealth("backup", zeusv1.PathHealth{Name: "backup", Transport: "rist", State: "down"})

	merged, err := m.Health(ctx)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if merged.State != "degraded" {
		t.Fatalf("merged state = %q, want degraded", merged.State)
	}
}

// TestMergerHealthAllDown verifies down state when all paths are down.
func TestMergerHealthAllDown(t *testing.T) {
	m := NewMerger()
	ctx := context.Background()

	m.UpdatePathHealth("primary", zeusv1.PathHealth{Name: "primary", Transport: "srt", State: "down"})

	merged, err := m.Health(ctx)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if merged.State != "down" {
		t.Fatalf("merged state = %q, want down", merged.State)
	}
}

// TestMergerStopAllEmpty verifies StopAll on an empty merger does not error.
func TestMergerStopAllEmpty(t *testing.T) {
	m := NewMerger()
	ctx := context.Background()
	if err := m.StopAll(ctx); err != nil {
		t.Fatalf("stop all: %v", err)
	}
}

// corev1SecretRef returns a corev1.SecretKeySelector (value type).
func corev1SecretRef(name, key string) corev1.SecretKeySelector {
	return corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: name}, Key: key}
}

// secretKeyRef returns a *corev1.SecretKeySelector (pointer type).
func secretKeyRef(name, key string) *corev1.SecretKeySelector {
	return &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: name}, Key: key}
}

var _ = fakeBridge{} // keep fakeBridge referenced for future use
var _ = corev1SecretRef
var _ = secretKeyRef
