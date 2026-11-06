package v1alpha1

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func validSRT() *SRTSpec {
	return &SRTSpec{
		Mode:                SRTCaller,
		RemoteHost:          "203.0.113.10",
		RemotePort:          9000,
		LatencyMs:           120,
		PBKeyLen:            32,
		PassphraseSecretRef: corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "srt-pass"}, Key: "passphrase"},
	}
}

// runbookWith builds a runbook with an explicit redundancy/mergeMode pair.
// The two are independent fields: hitless merge is selected by mergeMode, so
// tests that exercise the hitless rules must set both (as the spec examples do).
func runbookWith(paths []WANPathSpec, redundancy, mergeMode string) *ZeusRunbook {
	return &ZeusRunbook{
		ObjectMeta: metav1.ObjectMeta{Name: "rb", Namespace: "default"},
		Spec: ZeusRunbookSpec{
			EventClass: "test",
			Ingress: &IngressSpec{Contribution: &ContributionSpec{
				Redundancy: redundancy,
				MergeMode:  mergeMode,
				Paths:      paths,
			}},
		},
	}
}

func TestValidateContribution(t *testing.T) {
	tests := []struct {
		name    string
		rb      *ZeusRunbook
		wantErr string
	}{
		{
			name: "valid caller",
			rb:   runbookWith([]WANPathSpec{{Name: "primary", Transport: "srt", SRT: validSRT()}}, "none", "switchover"),
		},
		{
			name:    "caller without remote host",
			rb:      runbookWith([]WANPathSpec{{Name: "primary", Transport: "srt", SRT: func() *SRTSpec { s := validSRT(); s.RemoteHost = ""; return s }()}}, "none", "switchover"),
			wantErr: "remoteHost",
		},
		{
			name:    "caller with bad port",
			rb:      runbookWith([]WANPathSpec{{Name: "primary", Transport: "srt", SRT: func() *SRTSpec { s := validSRT(); s.RemotePort = 0; return s }()}}, "none", "switchover"),
			wantErr: "remotePort",
		},
		{
			name:    "invalid pbKeyLen",
			rb:      runbookWith([]WANPathSpec{{Name: "primary", Transport: "srt", SRT: func() *SRTSpec { s := validSRT(); s.PBKeyLen = 20; return s }()}}, "none", "switchover"),
			wantErr: "pbKeyLen",
		},
		{
			name:    "missing secret key",
			rb:      runbookWith([]WANPathSpec{{Name: "primary", Transport: "srt", SRT: func() *SRTSpec { s := validSRT(); s.PassphraseSecretRef.Key = ""; return s }()}}, "none", "switchover"),
			wantErr: "key",
		},
		{
			name:    "duplicate path names",
			rb:      runbookWith([]WANPathSpec{{Name: "a", Transport: "srt", SRT: validSRT()}, {Name: "a", Transport: "srt", SRT: validSRT()}}, "none", "switchover"),
			wantErr: "Duplicate",
		},
		{
			name:    "unsupported transport",
			rb:      runbookWith([]WANPathSpec{{Name: "primary", Transport: "rtmp", SRT: validSRT()}}, "none", "switchover"),
			wantErr: "Unsupported value",
		},
		{
			name:    "no paths",
			rb:      runbookWith(nil, "none", "switchover"),
			wantErr: "at least one WAN path",
		},
		{
			name:    "hitless with one path",
			rb:      runbookWith([]WANPathSpec{{Name: "primary", Transport: "srt", SRT: validSRT()}}, "hitless", "hitless"),
			wantErr: "at least 2 paths",
		},
		{
			name: "valid hitless with two paths",
			rb: runbookWith([]WANPathSpec{
				{Name: "a", Transport: "srt", SRT: validSRT()},
				{Name: "b", Transport: "srt", SRT: validSRT()},
			}, "hitless", "hitless"),
		},
		{
			name: "hitless RIST profile 1 is accepted",
			rb: runbookWith([]WANPathSpec{
				{Name: "a", Transport: "rist", RIST: &RISTSpec{Profile: 1, RemoteHost: "203.0.113.10", RemotePort: 9000}},
				{Name: "b", Transport: "rist", RIST: &RISTSpec{Profile: 1, RemoteHost: "203.0.113.11", RemotePort: 9000}},
			}, "hitless", "hitless"),
		},
		{
			name: "hitless RIST profile 0 is rejected",
			rb: runbookWith([]WANPathSpec{
				{Name: "a", Transport: "rist", RIST: &RISTSpec{Profile: 0, RemoteHost: "203.0.113.10", RemotePort: 9000}},
				{Name: "b", Transport: "rist", RIST: &RISTSpec{Profile: 0, RemoteHost: "203.0.113.11", RemotePort: 9000}},
			}, "hitless", "hitless"),
			wantErr: "profile 1",
		},
		{
			name: "listener without remote host",
			rb: runbookWith([]WANPathSpec{{Name: "primary", Transport: "srt", SRT: func() *SRTSpec {
				s := validSRT()
				s.Mode = SRTListener
				s.LocalPort = 9000
				s.RemoteHost = ""
				s.RemotePort = 0
				return s
			}()}}, "none", "switchover"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateContribution(tt.rb)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestValidateContributionAllowsNoIngress(t *testing.T) {
	rb := &ZeusRunbook{ObjectMeta: metav1.ObjectMeta{Name: "rb"}}
	if err := validateContribution(rb); err != nil {
		t.Fatalf("runbook without ingress must be accepted: %v", err)
	}
}
