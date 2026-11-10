package fabric

import (
	"context"
	"fmt"
	"log"
	"sync"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
)

// ClusterRef identifies a target Kubernetes cluster for deployment.
type ClusterRef struct {
	ID          string `json:"id"`          // unique cluster identifier
	Name        string `json:"name"`        // human-readable name
	Environment string `json:"environment"` // on-prem, cloud, hybrid
	KubeConfig  string `json:"kubeConfig"`  // path or inline kubeconfig YAML
	Context     string `json:"context"`     // kubeconfig context (optional)
	Region      string `json:"region,omitempty"`
	Zone        string `json:"zone,omitempty"`
	Capacity    int    `json:"capacity,omitempty"` // max NAP replicas on this cluster
}

// ClusterClient is a k8s client bound to a specific cluster.
type ClusterClient struct {
	ref       ClusterRef
	client    ctrl.Client
	k8s       kubernetes.Interface
	dynamic   dynamic.Interface
	discovery discovery.DiscoveryInterface
	scheme    *runtime.Scheme
}

// MultiClusterFabric manages NAP deployment across multiple k8s clusters.
// It is the compute-agnostic layer: Zeus does not care where a NAP runs,
// only that the target cluster advertises the required provider capability.
type MultiClusterFabric struct {
	mu      sync.RWMutex
	clusters map[string]*ClusterClient
	logger   *log.Logger
	defaultCluster string
}

// NewMultiClusterFabric creates a multi-cluster fabric with no clusters registered.
func NewMultiClusterFabric(logger *log.Logger) *MultiClusterFabric {
	return &MultiClusterFabric{
		clusters:  make(map[string]*ClusterClient),
		logger:    logger,
	}
}

// RegisterCluster adds a target cluster to the fabric. The kubeConfig can be
// a file path or inline YAML. If context is empty, the current context is used.
func (f *MultiClusterFabric) RegisterCluster(ref ClusterRef) error {
	var config *rest.Config

	// Try loading from file first.
	apiCfg, err := clientcmd.LoadFromFile(ref.KubeConfig)
	if err == nil {
		overrides := &clientcmd.ConfigOverrides{}
		if ref.Context != "" {
			overrides.CurrentContext = ref.Context
		}
		config, err = clientcmd.NewDefaultClientConfig(*apiCfg, overrides).ClientConfig()
		if err != nil {
			return fmt.Errorf("build rest config for cluster %s: %w", ref.ID, err)
		}
	} else {
		// Try inline YAML.
		cc, err := clientcmd.NewClientConfigFromBytes([]byte(ref.KubeConfig))
		if err != nil {
			return fmt.Errorf("load kubeconfig for cluster %s: %w", ref.ID, err)
		}
		config, err = cc.ClientConfig()
		if err != nil {
			return fmt.Errorf("client config for cluster %s: %w", ref.ID, err)
		}
	}

	// Increase QPS/burst for control-plane-heavy orchestration.
	config.QPS = 100
	config.Burst = 200

	// Increase QPS/burst for control-plane-heavy orchestration.
	config.QPS = 100
	config.Burst = 200

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return fmt.Errorf("add client-go scheme: %w", err)
	}
	if err := zeusv1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("add zeus scheme: %w", err)
	}

	c, err := ctrl.New(config, ctrl.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("build controller-runtime client for cluster %s: %w", ref.ID, err)
	}

	k8s, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("build kubernetes clientset for cluster %s: %w", ref.ID, err)
	}

	dyn, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("build dynamic client for cluster %s: %w", ref.ID, err)
	}

	disc, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return fmt.Errorf("build discovery client for cluster %s: %w", ref.ID, err)
	}

	cc := &ClusterClient{
		ref:       ref,
		client:    c,
		k8s:       k8s,
		dynamic:   dyn,
		discovery: disc,
		scheme:    scheme,
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.clusters[ref.ID] = cc
	if f.defaultCluster == "" {
		f.defaultCluster = ref.ID
	}
	f.logger.Printf("registered cluster %s (%s, env=%s)", ref.ID, ref.Name, ref.Environment)
	return nil
}

// GetCluster returns the cluster client by ID.
func (f *MultiClusterFabric) GetCluster(id string) (*ClusterClient, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	c, ok := f.clusters[id]
	if !ok {
		return nil, fmt.Errorf("cluster %s not registered", id)
	}
	return c, nil
}

// DefaultCluster returns the default cluster client.
func (f *MultiClusterFabric) DefaultCluster() (*ClusterClient, error) {
	return f.GetCluster(f.defaultCluster)
}

// ListClusters returns all registered clusters.
func (f *MultiClusterFabric) ListClusters() []ClusterRef {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]ClusterRef, 0, len(f.clusters))
	for _, cc := range f.clusters {
		out = append(out, cc.ref)
	}
	return out
}

// SetDefault sets the default cluster for deployments.
func (f *MultiClusterFabric) SetDefault(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.clusters[id]; !ok {
		return fmt.Errorf("cluster %s not registered", id)
	}
	f.defaultCluster = id
	f.logger.Printf("default cluster set to %s", id)
	return nil
}

// EnsureReceiver is part of the fabric.Client interface; it delegates to the
// default cluster. For multi-cluster, callers should use EnsureReceiverOn.
func (f *MultiClusterFabric) EnsureReceiver(ctx context.Context, namespace, name, flowID, podName, provider string, owner *zeusv1.ZeusRunbook) error {
	c, err := f.DefaultCluster()
	if err != nil {
		return err
	}
	return c.EnsureReceiver(ctx, namespace, name, flowID, podName, provider, owner)
}

// DeleteReceiver is part of the fabric.Client interface; delegates to default cluster.
func (f *MultiClusterFabric) DeleteReceiver(ctx context.Context, namespace, name string) error {
	c, err := f.DefaultCluster()
	if err != nil {
		return err
	}
	return c.DeleteReceiver(ctx, namespace, name)
}

// EnsureReceiverOn deploys an MXL receiver on a specific cluster.
func (f *MultiClusterFabric) EnsureReceiverOn(clusterID string, ctx context.Context, namespace, name, flowID, podName, provider string, owner *zeusv1.ZeusRunbook) error {
	c, err := f.GetCluster(clusterID)
	if err != nil {
		return err
	}
	return c.EnsureReceiver(ctx, namespace, name, flowID, podName, provider, owner)
}

// DeleteReceiverOn deletes an MXL receiver on a specific cluster.
func (f *MultiClusterFabric) DeleteReceiverOn(clusterID string, ctx context.Context, namespace, name string) error {
	c, err := f.GetCluster(clusterID)
	if err != nil {
		return err
	}
	return c.DeleteReceiver(ctx, namespace, name)
}

// RequireProviderOn validates that a node on the given cluster advertises the provider.
func (f *MultiClusterFabric) RequireProviderOn(clusterID string, ctx context.Context, nodeName, provider string) error {
	c, err := f.GetCluster(clusterID)
	if err != nil {
		return err
	}
	return RequireProvider(ctx, c.client, nodeName, provider)
}

// EnsureReceiver on ClusterClient wraps the RealClient logic for this cluster.
func (cc *ClusterClient) EnsureReceiver(ctx context.Context, namespace, name, flowID, podName, provider string, owner *zeusv1.ZeusRunbook) error {
	real := NewRealClient(cc.client)
	return real.EnsureReceiver(ctx, namespace, name, flowID, podName, provider, owner)
}

// DeleteReceiver on ClusterClient wraps the RealClient logic for this cluster.
func (cc *ClusterClient) DeleteReceiver(ctx context.Context, namespace, name string) error {
	real := NewRealClient(cc.client)
	return real.DeleteReceiver(ctx, namespace, name)
}


