package srt

import (
	"context"
	"net"
	"strconv"
	"testing"

	corev1 "k8s.io/api/core/v1"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
)

// echoListener accepts one connection, reads a byte, and writes it back.
func echoListener(t *testing.T, l net.Listener) {
	t.Helper()
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var buf [1]byte
		if _, err := conn.Read(buf[:]); err != nil {
			return
		}
		conn.Write(buf[:])
	}()
}

func TestBridgeHealthUp(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()
	echoListener(t, l)

	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	b := NewBridge()
	ctx := context.Background()
	spec := zeusv1.SRTSpec{
		Mode:                zeusv1.SRTCaller,
		RemoteHost:          "127.0.0.1",
		RemotePort:          mustParsePort(port),
		PBKeyLen:            32,
		PassphraseSecretRef: corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "srt-test"}, Key: "passphrase"},
	}
	if err := b.Start(ctx, spec, []byte("this-is-a-test-passphrase-20")); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer b.Stop(ctx)

	ph, err := b.Health(ctx)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if ph.State != "up" {
		t.Fatalf("state = %q, want up", ph.State)
	}
	if ph.Transport != "srt" {
		t.Fatalf("transport = %q, want srt", ph.Transport)
	}
	if ph.RTTMs < 0 {
		t.Fatalf("rttMs = %d, want >= 0", ph.RTTMs)
	}
}

func TestBridgeHealthDown(t *testing.T) {
	b := NewBridge()
	ctx := context.Background()
	spec := zeusv1.SRTSpec{
		Mode:                zeusv1.SRTCaller,
		RemoteHost:          "127.0.0.1",
		RemotePort:          1,
		PBKeyLen:            32,
		PassphraseSecretRef: corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "srt-test"}, Key: "passphrase"},
	}
	// Start should fail because nothing is listening on port 1.
	if err := b.Start(ctx, spec, []byte("this-is-a-test-passphrase-20")); err == nil {
		t.Fatal("expected start to fail on refused port")
	}

	// Health without a successful Start should report down.
	ph, err := b.Health(ctx)
	if err == nil {
		t.Fatal("expected health error when not started")
	}
	if ph.State != "down" {
		t.Fatalf("state = %q, want down", ph.State)
	}
}

func mustParsePort(s string) int32 {
	p, err := strconv.Atoi(s)
	if err != nil {
		panic(err)
	}
	return int32(p)
}
