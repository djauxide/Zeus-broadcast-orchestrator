package fabric

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
)

var (
	mxlReceiverGVK         = schema.GroupVersionKind{Group: "mxl.qvest-digital.com", Version: "v1alpha1", Kind: "MxlReceiver"}
	mxlNodeCapabilitiesGVK = schema.GroupVersionKind{Group: "mxl.qvest-digital.com", Version: "v1alpha1", Kind: "MxlNodeCapabilities"}
)

type RealClient struct{ c client.Client }

func NewRealClient(c client.Client) *RealClient { return &RealClient{c: c} }

func (r *RealClient) EnsureReceiver(ctx context.Context, namespace, name, flowID, podName, provider string, owner *zeusv1.ZeusRunbook) error {
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(mxlReceiverGVK)
	if err := r.c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, existing); err == nil {
		return nil
	}

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(mxlReceiverGVK)
	obj.SetNamespace(namespace)
	obj.SetName(name)
	if err := controllerutil.SetControllerReference(owner, obj, r.c.Scheme()); err != nil {
		return fmt.Errorf("set controller ref: %w", err)
	}
	obj.Object["spec"] = map[string]interface{}{
		"flowID":   flowID,
		"provider": provider,
		"podRef":   map[string]interface{}{"name": podName, "namespace": namespace},
	}
	return r.c.Create(ctx, obj)
}

func (r *RealClient) DeleteReceiver(ctx context.Context, namespace, name string) error {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(mxlReceiverGVK)
	obj.SetNamespace(namespace)
	obj.SetName(name)
	if err := r.c.Delete(ctx, obj); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("delete mxlreceiver: %w", err)
	}
	return nil
}

func RequireProvider(ctx context.Context, c client.Client, nodeName, provider string) error {
	nc := &unstructured.Unstructured{}
	nc.SetGroupVersionKind(mxlNodeCapabilitiesGVK)
	if err := c.Get(ctx, client.ObjectKey{Name: nodeName}, nc); err != nil {
		return fmt.Errorf("node %s has no MxlNodeCapabilities: %w", nodeName, err)
	}
	providers, _, _ := unstructured.NestedStringSlice(nc.Object, "status", "providers")
	for _, p := range providers {
		if p == provider {
			return nil
		}
	}
	return fmt.Errorf("node %s does not advertise provider %q (has %v)", nodeName, provider, providers)
}
