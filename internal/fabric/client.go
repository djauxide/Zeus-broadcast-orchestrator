package fabric

import (
	"context"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
)

type Client interface {
	EnsureReceiver(ctx context.Context, namespace, name, flowID, podName, provider string, owner *zeusv1.ZeusRunbook) error
	DeleteReceiver(ctx context.Context, namespace, name string) error
}
