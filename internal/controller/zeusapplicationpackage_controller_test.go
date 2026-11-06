package controller

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
)

func reconcileNAP(t *testing.T, nap *zeusv1.ZeusApplicationPackage) *zeusv1.ZeusApplicationPackage {
	t.Helper()
	s := testScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&zeusv1.ZeusApplicationPackage{}).
		WithObjects(nap).
		Build()
	r := &ZeusApplicationPackageReconciler{Client: c, Scheme: s}
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

func TestNAPValidationAcceptsCompletePackage(t *testing.T) {
	out := reconcileNAP(t, testNAP("mixer", "", 1))
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
	out := reconcileNAP(t, nap)
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
	out := reconcileNAP(t, nap)
	if out.Status.Phase != "Rejected" {
		t.Fatalf("phase = %q, want Rejected after spec change", out.Status.Phase)
	}
}

func TestNAPIsNotRereconciledWithoutSpecChange(t *testing.T) {
	nap := testNAP("mixer", "Validated", 2)
	nap.Generation = 2
	nap.Spec.ImageSignature = "" // invalid, but generation is unchanged
	out := reconcileNAP(t, nap)
	if out.Status.Phase != "Validated" {
		t.Fatalf("phase = %q, want Validated (no revalidation without a spec change)", out.Status.Phase)
	}
}
