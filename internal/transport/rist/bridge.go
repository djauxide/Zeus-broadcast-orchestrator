package rist

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"time"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
)

// Bridge is the WAN-facing RIST leg. RIST profile 1 (Main) is ST 2022-7
// capable and supports hitless merge with redundant paths. The passphrase must
// be resolved from the referenced Secret BEFORE Start; the Bridge never reads
// Secrets directly.
type Bridge interface {
	Start(ctx context.Context, spec zeusv1.RISTSpec, passphrase []byte) error
	Health(ctx context.Context) (zeusv1.PathHealth, error)
	Stop(ctx context.Context) error
}

// ristBridge is a Phase 2 concrete implementation of RIST Bridge. It opens a
// real TCP socket to the RIST peer's control port to measure path connectivity
// and round-trip time. Without librist this is a TCP-level probe: it measures
// reachability and RTT, not RIST-specific redundancy statistics.
//
// RIST profile 1 is required for hitless merge because it provides the ST
// 2022-7 redundant stream encapsulation that allows packet-level combining.
type ristBridge struct {
	spec       zeusv1.RISTSpec
	passphrase []byte
	started    bool
	conn       net.Conn
}

// Compile-time check that ristBridge implements Bridge.
var _ Bridge = (*ristBridge)(nil)

// NewBridge returns a Bridge backed by a real TCP socket probe to the RIST
// peer. It does not connect until Start is called.
func NewBridge() Bridge { return &ristBridge{} }

// Start validates the RIST spec and establishes a TCP connection to the RIST
// peer's remoteHost:remotePort. The connection is kept open so Health can
// reuse it for RTT measurement.
func (b *ristBridge) Start(ctx context.Context, spec zeusv1.RISTSpec, passphrase []byte) error {
	if spec.RemoteHost == "" || spec.RemotePort <= 0 {
		return errors.New("rist: remoteHost and remotePort are required")
	}
	if spec.Profile < 0 || spec.Profile > 2 {
		return errors.New("rist: profile must be 0, 1, or 2")
	}
	if spec.Profile != 1 {
		return errors.New("rist: hitless merge requires profile 1 (Main), which is ST 2022-7 capable")
	}
	if len(passphrase) < 20 {
		return errors.New("rist: passphrase must be at least 20 characters")
	}

	d := net.Dialer{Timeout: 5 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(spec.RemoteHost, strconv.Itoa(int(spec.RemotePort))))
	if err != nil {
		return err
	}
	b.spec = spec
	b.passphrase = append([]byte(nil), passphrase...)
	b.conn = conn
	b.started = true
	return nil
}

// Health measures real path connectivity and RTT by writing a probe byte over
// the established TCP connection and timing the round trip.
func (b *ristBridge) Health(ctx context.Context) (zeusv1.PathHealth, error) {
	if !b.started || b.conn == nil {
		return zeusv1.PathHealth{
			Name:      b.spec.RemoteHost,
			Transport: "rist",
			State:     "down",
		}, errors.New("rist: bridge not started")
	}

	deadline := time.Now().Add(5 * time.Second)
	if err := b.conn.SetDeadline(deadline); err != nil {
		return zeusv1.PathHealth{}, err
	}

	start := time.Now()
	if _, err := b.conn.Write([]byte{0}); err != nil {
		b.conn.Close()
		b.conn = nil
		b.started = false
		return zeusv1.PathHealth{
			Name:      b.spec.RemoteHost,
			Transport: "rist",
			State:     "down",
		}, err
	}
	var buf [1]byte
	if _, err := io.ReadFull(b.conn, buf[:]); err != nil {
		rtt := time.Since(start)
		return zeusv1.PathHealth{
			Name:      b.spec.RemoteHost,
			Transport: "rist",
			State:     "degraded",
			RTTMs:     int32(rtt / time.Millisecond),
		}, nil
	}
	rtt := time.Since(start)
	return zeusv1.PathHealth{
		Name:      b.spec.RemoteHost,
		Transport: "rist",
		State:     "up",
		RTTMs:     int32(rtt / time.Millisecond),
	}, nil
}

// Stop closes the underlying TCP connection.
func (b *ristBridge) Stop(ctx context.Context) error {
	if b.conn != nil {
		err := b.conn.Close()
		b.conn = nil
		b.started = false
		return err
	}
	return nil
}
