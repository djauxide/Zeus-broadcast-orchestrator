package controller

import (
	"context"
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
	"github.com/zeus-broadcast/zeus-kernel/internal/nmos"
)

func reconcileNAP(t *testing.T, nap *zeusv1.ZeusApplicationPackage, nmosClient NMOSClient) *zeusv1.ZeusApplicationPackage {
	t.Helper()
	s := testScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&zeusv1.ZeusApplicationPackage{}).
		WithObjects(nap).
		Build()
	r := &ZeusApplicationPackageReconciler{Client: c, Scheme: s, NMOS: nmosClient}
	ctx := context.Background()
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: nap.Namespace, Name: nap.Name}}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	var out zeusv1.ZeusApplicationPackage
	if err := c.Get(ctx, req.NamespacedName, &out); err != nil {
		t.Fatalf("get: %v", err)
	}
	return &out
}

// fakeNMOSClient implements NMOSClient for tests.
type fakeNMOSClient struct {
	sender *nmos.Resource
	err    error
}

func (f *fakeNMOSClient) FindSender(ctx context.Context, match nmos.SenderMatch) (*nmos.Resource, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.sender, nil
}

func TestNAPValidationAcceptsCompletePackage(t *testing.T) {
	out := reconcileNAP(t, testNAP("mixer", "", 1), nil)
	if out.Status.Phase != "Validated" {
		t.Fatalf("phase = %q, want Validated", out.Status.Phase)
	}
	if out.Status.ObservedGen != out.Generation {
		t.Fatalf("observedGen = %d, generation = %d", out.Status.ObservedGen, out.Generation)
	}
}

func TestNAPValidationRejectsMissingSignature(t *testing.T) {
	nap := testNAP("mixer", "", 1)
	nap.Spec.ImageSignature = ""
	out := reconcileNAP(t, nap, nil)
	if out.Status.Phase != "Rejected" {
		t.Fatalf("phase = %q, want Rejected", out.Status.Phase)
	}
	if out.Status.ObservedGen != out.Generation {
		t.Fatalf("rejected package must record observedGeneration")
	}
}

// A spec edit increments metadata.generation; the old verdict must not stick.
func TestNAPIsRevalidatedAfterSpecChange(t *testing.T) {
	nap := testNAP("mixer", "Validated", 1)
	nap.Generation = 2
	nap.Spec.ImageSignature = "" // now invalid
	out := reconcileNAP(t, nap, nil)
	if out.Status.Phase != "Rejected" {
		t.Fatalf("phase = %q, want Rejected after spec change", out.Status.Phase)
	}
}

func TestNAPIsNotRereconciledWithoutSpecChange(t *testing.T) {
	nap := testNAP("mixer", "Validated", 2)
	nap.Generation = 2
	nap.Spec.ImageSignature = "" // invalid, but generation is unchanged
	out := reconcileNAP(t, nap, nil)
	if out.Status.Phase != "Validated" {
		t.Fatalf("phase = %q, want Validated (no revalidation without a spec change)", out.Status.Phase)
	}
}

// TestNAPValidationRejectsWhenNoNMOSClient verifies that when senderMatch is
// enabled but no NMOS client is configured, the NAP is rejected.
func TestNAPValidationRejectsWhenNoNMOSClient(t *testing.T) {
	nap := testNAP("mixer", "", 1)
	nap.Spec.NMOS.IS04 = true
	nap.Spec.NMOS.SenderMatch = true
	out := reconcileNAP(t, nap, nil)
	if out.Status.Phase != "Rejected" {
		t.Fatalf("phase = %q, want Rejected when NMOS client not configured", out.Status.Phase)
	}
}

// TestNAPValidationAcceptsWhenSenderFound verifies that when a matching IS-04
// sender is found, the NAP is validated.
func TestNAPValidationAcceptsWhenSenderFound(t *testing.T) {
	nap := testNAP("mixer", "", 1)
	nap.Spec.NMOS.IS04 = true
	nap.Spec.NMOS.SenderMatch = true
	nap.Spec.NMOS.MediaType = "video/raw"
	nap.Spec.NMOS.Essence = "raw"
	nap.Spec.NMOS.Sampling = "YCbCr:4:2:2:10bit"

	sender := &nmos.Resource{
		ID:   "sender-1",
		Label: "Camera 1 Sender",
		Type:  "sender",
		Manifest: nmos.Manifest{
			MediaType:   "video/raw",
			Essence:     "raw",
			Sampling:    "YCbCr:4:2:2:10bit",
		},
	}
	fakeNMOS := &fakeNMOSClient{sender: sender}

	out := reconcileNAP(t, nap, fakeNMOS)
	if out.Status.Phase != "Validated" {
		t.Fatalf("phase = %q, want Validated when sender found", out.Status.Phase)
	}
}

// TestNAPValidationRejectsWhenNoSenderFound verifies that when no matching
// IS-04 sender is found, the NAP is rejected.
func TestNAPValidationRejectsWhenNoSenderFound(t *testing.T) {
	nap := testNAP("mixer", "", 1)
	nap.Spec.NMOS.IS04 = true
	nap.Spec.NMOS.SenderMatch = true
	nap.Spec.NMOS.MediaType = "audio/raw"

	fakeNMOS := &fakeNMOSClient{sender: nil}

	out := reconcileNAP(t, nap, fakeNMOS)
	if out.Status.Phase != "Rejected" {
		t.Fatalf("phase = %q, want Rejected when no sender found", out.Status.Phase)
	}
}

// TestNAPValidationRejectsWhenNMOSRegistryErrors verifies that NMOS registry
// errors cause rejection.
func TestNAPValidationRejectsWhenNMOSRegistryErrors(t *testing.T) {
	nap := testNAP("mixer", "", 1)
	nap.Spec.NMOS.IS04 = true
	nap.Spec.NMOS.SenderMatch = true

	fakeNMOS := &fakeNMOSClient{err: errors.New("registry unreachable")}

	out := reconcileNAP(t, nap, fakeNMOS)
	if out.Status.Phase != "Rejected" {
		t.Fatalf("phase = %q, want Rejected when NMOS registry errors", out.Status.Phase)
	}
}
