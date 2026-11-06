package controller

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
	"github.com/zeus-broadcast/zeus-kernel/internal/fabric"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("add clientgo scheme: %v", err)
	}
	if err := zeusv1.AddToScheme(s); err != nil {
		t.Fatalf("add zeus scheme: %v", err)
	}
	return s
}

func testRunbook(name string, nap string) *zeusv1.ZeusRunbook {
	return &zeusv1.ZeusRunbook{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       types.UID("11111111-1111-1111-1111-111111111111"),
		},
		Spec: zeusv1.ZeusRunbookSpec{
			EventClass: "test",
			NAPs:       []zeusv1.NAPPlacement{{Name: name + "-mixer", NAP: nap}},
		},
	}
}

func testNAP(name, phase string, observedGen int64) *zeusv1.ZeusApplicationPackage {
	return &zeusv1.ZeusApplicationPackage{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Generation: observedGen},
		Spec: zeusv1.ZeusApplicationPackageSpec{
			Image:          "example.invalid/mixer:1",
			ImageSignature: "cosign:test",
			CRM: zeusv1.CRMResources{
				CPU:     "1",
				Memory:  "1Gi",
				MediaIO: zeusv1.MediaIO{DomainRef: "domain-a", MXLWriter: true},
			},
			NMOS: zeusv1.NMOSSpec{Transport: "urn:x-nmos:transport:mxl"},
		},
		Status: zeusv1.ZeusApplicationPackageStatus{Phase: phase, ObservedGen: observedGen},
	}
}

func testPod(app string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      app + "-pod",
			Namespace: "default",
			Labels:    map[string]string{"app": app},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "media", Image: "example.invalid/mixer:1"}}},
	}
}

func newReconciler(t *testing.T, objs ...client.Object) (*ZeusRunbookReconciler, *fabric.FakeClient) {
	t.Helper()
	s := testScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&zeusv1.ZeusRunbook{}, &zeusv1.ZeusApplicationPackage{}).
		WithObjects(objs...).
		Build()
	f := fabric.NewFakeClient()
	return &ZeusRunbookReconciler{Client: c, Scheme: s, Fabric: f}, f
}

func reconcileRunbook(t *testing.T, r *ZeusRunbookReconciler, name string) (*zeusv1.ZeusRunbook, error) {
	t.Helper()
	ctx := context.Background()
	_, err := r.Reconcile(ctx, request(name))
	if err != nil {
		return nil, err
	}
	var out zeusv1.ZeusRunbook
	if err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: name}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func request(name string) reconcile.Request {
	return reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: name}}
}

func TestReconcileReachesLiveAndBindsReceiver(t *testing.T) {
	rb := testRunbook("show", "mixer")
	r, fab := newReconciler(t, rb, testNAP("mixer", "Validated", 1), testPod("show-mixer"))

	out, err := reconcileRunbook(t, r, "show")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if out.Status.Phase != "Live" {
		t.Fatalf("phase = %q, want Live (reason %q)", out.Status.Phase, out.Status.Conditions[0].Reason)
	}
	if _, ok := fab.Get("default", "show-mixer"); !ok {
		t.Fatal("receiver was not ensured")
	}
	var dep appsv1.Deployment
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "show-mixer"}, &dep); err != nil {
		t.Fatalf("deployment missing: %v", err)
	}
}

func TestReconcileWaitsForValidatedNAP(t *testing.T) {
	rb := testRunbook("show", "mixer")
	r, fab := newReconciler(t, rb, testNAP("mixer", "", 0))

	out, err := reconcileRunbook(t, r, "show")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if out.Status.Phase != "Reconciling" {
		t.Fatalf("phase = %q, want Reconciling", out.Status.Phase)
	}
	if out.Status.Conditions[0].Reason != "NAPNotValidated" {
		t.Fatalf("reason = %q, want NAPNotValidated", out.Status.Conditions[0].Reason)
	}
	if _, ok := fab.Get("default", "show-mixer"); ok {
		t.Fatal("receiver must not be created before the NAP is validated")
	}
}

// A NAP becoming ready after the runbook was applied must wake the runbook.
func TestNAPEventMapsToReferencingRunbooks(t *testing.T) {
	match := testRunbook("match", "mixer")
	match.Spec.NAPs = []zeusv1.NAPPlacement{{Name: "match-mixer", NAP: "mixer"}}
	other := testRunbook("other", "different")
	r, _ := newReconciler(t, match, other, testNAP("mixer", "Validated", 1))

	reqs := r.runbooksForNAP(context.Background(), testNAP("mixer", "Validated", 1))
	if len(reqs) != 1 || reqs[0].Name != "match" {
		t.Fatalf("requests = %+v, want only runbook match", reqs)
	}
}

func TestTeardownRemovesFabricAndDeployment(t *testing.T) {
	rb := testRunbook("show", "mixer")
	rb.Finalizers = []string{zeusFinalizer}
	rb.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	r, fab := newReconciler(t, rb, testNAP("mixer", "Validated", 1), testPod("show-mixer"))

	if err := fab.EnsureReceiver(context.Background(), "default", "show-mixer", "flow", "pod", "tcp", nil); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := r.Reconcile(ctx, request("show")); err != nil {
		t.Fatalf("reconcile delete: %v", err)
	}
	if _, ok := fab.Get("default", "show-mixer"); ok {
		t.Fatal("receiver was not deleted")
	}
	var dep appsv1.Deployment
	err := r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "show-mixer"}, &dep)
	if err == nil {
		t.Fatal("deployment still present after teardown")
	}
	var out zeusv1.ZeusRunbook
	err = r.Get(ctx, types.NamespacedName{Namespace: "default", Name: "show"}, &out)
	if err == nil {
		t.Fatal("runbook still present after finalizer removal")
	}
}
