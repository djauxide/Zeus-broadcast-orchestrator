package conversion_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
)

func TestRunbookRoundTrip(t *testing.T) {
	// envtest needs the generated CRDs or the ZeusRunbook Create will fail
	// with "no matches for kind". `make manifests` must run first.
	env := &envtest.Environment{CRDDirectoryPaths: []string{filepath.Join("..", "..", "config", "crd", "bases")}}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	defer env.Stop()

	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = zeusv1.AddToScheme(scheme)
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	in := &zeusv1.ZeusRunbook{
		ObjectMeta: metav1.ObjectMeta{Name: "rt", Namespace: "default"},
		Spec: zeusv1.ZeusRunbookSpec{
			EventClass: "rt",
			NAPs:       []zeusv1.NAPPlacement{{Name: "vm", NAP: "vm"}},
		},
	}
	ctx := context.Background()
	if err := c.Create(ctx, in); err != nil {
		t.Fatalf("create: %v", err)
	}
	out := &zeusv1.ZeusRunbook{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(in), out); err != nil {
		t.Fatalf("get: %v", err)
	}
	if !reflect.DeepEqual(in.Spec, out.Spec) {
		t.Fatalf("spec drift: in=%+v out=%+v", in.Spec, out.Spec)
	}
}
