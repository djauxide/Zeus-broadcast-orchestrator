package fabric

import (
	"context"
	"fmt"
	"log"
	"sync"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Scaler dynamically spins NAP deployments up or down on demand across
// registered clusters. It integrates with Kubernetes HPA and cluster
// autoscaler to provide burst capacity on cloud clusters when on-prem
// is saturated.
type Scaler struct {
	mu     sync.Mutex
	fabric *MultiClusterFabric
	logger *log.Logger
}

// NewScaler creates a fabric scaler.
func NewScaler(fabric *MultiClusterFabric, logger *log.Logger) *Scaler {
	return &Scaler{fabric: fabric, logger: logger}
}

// ScaleNAP adjusts the replica count of a NAP deployment on a specific cluster.
// If targetReplicas is 0, the deployment is scaled down (not deleted).
func (s *Scaler) ScaleNAP(ctx context.Context, clusterID, namespace, name string, targetReplicas int32) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cluster, err := s.fabric.GetCluster(clusterID)
	if err != nil {
		return err
	}

	deploy := &appsv1.Deployment{}
	deploy.Name = name
	deploy.Namespace = namespace

	// Patch the deployment spec replicas.
	patch := []byte(fmt.Sprintf(`{"spec":{"replicas":%d}}`, targetReplicas))
	if err := cluster.client.Patch(ctx, deploy, ctrlclient.RawPatch(types.MergePatchType, patch)); err != nil {
		return fmt.Errorf("scale deployment %s/%s on cluster %s to %d: %w", namespace, name, clusterID, targetReplicas, err)
	}

	s.logger.Printf("SCALE deployment %s/%s on cluster %s -> %d replicas", namespace, name, clusterID, targetReplicas)
	return nil
}

// ScaleNAPOnDefault scales a NAP on the default cluster.
func (s *Scaler) ScaleNAPOnDefault(ctx context.Context, namespace, name string, targetReplicas int32) error {
	clusterID, err := s.fabric.DefaultClusterID()
	if err != nil {
		return err
	}
	return s.ScaleNAP(ctx, clusterID, namespace, name, targetReplicas)
}

// DefaultClusterID returns the ID of the default cluster.
func (f *MultiClusterFabric) DefaultClusterID() (string, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.defaultCluster == "" {
		return "", fmt.Errorf("no default cluster set")
	}
	return f.defaultCluster, nil
}

// BurstToCloud evaluates whether to burst NAP replicas to a cloud cluster
// when on-prem is at capacity. Returns the cluster ID chosen and the number
// of replicas to place there.
func (s *Scaler) BurstToCloud(ctx context.Context, namespace, napName string, onPremReplicas, cloudReplicas int32) (string, int32, error) {
	clusters := s.fabric.ListClusters()
	var cloudClusterID string
	for _, c := range clusters {
		if c.Environment == "cloud" {
			cloudClusterID = c.ID
			break
		}
	}
	if cloudClusterID == "" {
		return "", 0, fmt.Errorf("no cloud cluster registered for burst")
	}

	// Scale down on-prem if requested, scale up cloud.
	if err := s.ScaleNAP(ctx, "on-prem", namespace, napName, onPremReplicas); err != nil {
		return "", 0, err
	}
	if err := s.ScaleNAP(ctx, cloudClusterID, namespace, napName, cloudReplicas); err != nil {
		return "", 0, err
	}

	s.logger.Printf("BURST nap=%s namespace=%s -> cloud cluster %s (%d replicas)", napName, namespace, cloudClusterID, cloudReplicas)
	return cloudClusterID, cloudReplicas, nil
}

// CreateHPA creates a HorizontalPodAutoscaler for a NAP deployment so it
// scales dynamically based on CPU/media load.
func (s *Scaler) CreateHPA(ctx context.Context, clusterID, namespace, deployName string, min, max int32) error {
	cluster, err := s.fabric.GetCluster(clusterID)
	if err != nil {
		return err
	}

	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deployName + "-hpa",
			Namespace: namespace,
		},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       deployName,
			},
			MinReplicas: &min,
			MaxReplicas: max,
			Metrics: []autoscalingv2.MetricSpec{
				{
					Type: autoscalingv2.ResourceMetricSourceType,
					Resource: &autoscalingv2.ResourceMetricSource{
						Name: corev1.ResourceCPU,
						Target: autoscalingv2.MetricTarget{
							Type:               autoscalingv2.UtilizationMetricType,
							AverageUtilization: ptrInt32(70),
						},
					},
				},
			},
		},
	}

	if err := cluster.client.Create(ctx, hpa); err != nil {
		return fmt.Errorf("create HPA for deployment %s/%s: %w", namespace, deployName, err)
	}
	s.logger.Printf("CREATED HPA %s/%s min=%d max=%d", namespace, hpa.Name, min, max)
	return nil
}

func ptrInt32(v int32) *int32 { return &v }


