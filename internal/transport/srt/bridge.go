package srt

import (
	"context"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
)

// Bridge is the WAN-facing SRT leg. The MVP uses a single caller; Phase 2 adds
// listener, rendezvous, and dual-path hitless merge. The passphrase must be
// resolved from the referenced Secret BEFORE Start; the Bridge never reads
// Secrets directly.
type Bridge interface {
	Start(ctx context.Context, spec zeusv1.SRTSpec, passphrase []byte) error
	Health(ctx context.Context) (zeusv1.PathHealth, error)
	Stop(ctx context.Context) error
}
