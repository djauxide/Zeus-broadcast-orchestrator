
package registry

import (
	"context"
)

// RegistryMCP exposes IS-04 query and IS-05 connect/disconnect as MCP tools.
// Authorization is enforced IN CODE before any request leaves the server.
type RegistryMCP interface {
	QueryReceivers(ctx context.Context) ([]map[string]interface{}, error)
	Connect(ctx context.Context, senderID, receiverID string) error
	Disconnect(ctx context.Context, receiverID string) error
}
