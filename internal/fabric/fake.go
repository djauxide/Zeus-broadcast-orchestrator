
package fabric

import (
	"context"
	"sync"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
)

type FakeClient struct {
	mu        sync.Mutex
	receivers map[string]string
}

func NewFakeClient() *FakeClient { return &FakeClient{receivers: map[string]string{}} }

func (f *FakeClient) EnsureReceiver(_ context.Context, namespace, name, flowID, _, _ string, _ *zeusv1.ZeusRunbook) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.receivers[namespace+"/"+name] = flowID
	return nil
}

func (f *FakeClient) DeleteReceiver(_ context.Context, namespace, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.receivers, namespace+"/"+name)
	return nil
}

func (f *FakeClient) Get(namespace, name string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.receivers[namespace+"/"+name]
	return v, ok
}
