package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clilog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
	"github.com/zeus-broadcast/zeus-kernel/internal/mcp/transport"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"fmt"
)

// RegistryMCPServer implements the RegistryMCP interface over MCP stdio transport.
type RegistryMCPServer struct {
	client.Client
	scheme      *runtime.Scheme
	nmosBaseURL string
}

func NewRegistryMCPServer(c client.Client, scheme *runtime.Scheme, nmosBaseURL string) *RegistryMCPServer {
	return &RegistryMCPServer{
		Client:      c,
		scheme:      scheme,
		nmosBaseURL: nmosBaseURL,
	}
}

func (s *RegistryMCPServer) Run(ctx context.Context) error {
	srv := transport.NewServer(os.Stdin, os.Stdout)
	srv.WithLogger(log.New(os.Stderr, "[mcp-registry] ", log.LstdFlags|log.Lmicroseconds))

	srv.Handle("initialize", s.handleInitialize)
	srv.Handle("tools/list", s.handleToolsList)
	srv.Handle("tools/call", s.handleToolsCall)
	srv.Handle("notifications/initialized", s.handleInitialized)
	srv.Handle("ping", s.handlePing)

	return srv.Start(ctx)
}

func (s *RegistryMCPServer) handleInitialize(ctx context.Context, params json.RawMessage) (interface{}, error) {
	return map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]interface{}{
			"tools": map[string]interface{}{},
		},
		"serverInfo": map[string]interface{}{
			"name":    "zeus-mcp-registry",
			"version": "0.1.0",
		},
	}, nil
}

func (s *RegistryMCPServer) handleToolsList(ctx context.Context, params json.RawMessage) (interface{}, error) {
	tools := []map[string]interface{}{
		{
			"name":        "nmos_query_receivers",
			"description": "Query IS-04 receivers from the NMOS registry",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"nodeName": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "nmos_connect",
			"description": "Connect a sender to a receiver via IS-05",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"senderID":   map[string]interface{}{"type": "string"},
					"receiverID": map[string]interface{}{"type": "string"},
				},
				"required": []string{"senderID", "receiverID"},
			},
		},
		{
			"name":        "nmos_disconnect",
			"description": "Disconnect a receiver via IS-05",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"receiverID": map[string]interface{}{"type": "string"},
				},
				"required": []string{"receiverID"},
			},
		},
	}
	return map[string]interface{}{
		"tools": tools,
	}, nil
}

func (s *RegistryMCPServer) handleToolsCall(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}

	switch call.Name {
	case "nmos_query_receivers":
		return s.handleQueryReceivers(ctx, call.Arguments)
	case "nmos_connect":
		return s.handleConnect(ctx, call.Arguments)
	case "nmos_disconnect":
		return s.handleDisconnect(ctx, call.Arguments)
	default:
		return nil, fmt.Errorf("unknown tool: %s", call.Name)
	}
}

func (s *RegistryMCPServer) handleInitialized(ctx context.Context, params json.RawMessage) (interface{}, error) {
	return nil, nil
}

func (s *RegistryMCPServer) handlePing(ctx context.Context, params json.RawMessage) (interface{}, error) {
	return nil, nil
}

func (s *RegistryMCPServer) handleQueryReceivers(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var args struct {
		NodeName string `json:"nodeName,omitempty"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}

	receiversGVK := schema.GroupVersionKind{
		Group:   "mxl.qvest-digital.com",
		Version: "v1alpha1",
		Kind:    "MxlReceiver",
	}

	uv := &unstructured.UnstructuredList{}
	uv.SetGroupVersionKind(receiversGVK)
	if err := s.List(ctx, uv); err != nil {
		return nil, fmt.Errorf("list receivers: %w", err)
	}

	receivers := make([]map[string]interface{}, 0, len(uv.Items))
	for _, item := range uv.Items {
		flowID, _, _ := unstructured.NestedString(item.Object, "spec", "flowID")
		nodeName, _, _ := unstructured.NestedString(item.Object, "spec", "podRef", "name")
		provider, _, _ := unstructured.NestedString(item.Object, "spec", "provider")
		status, _, _ := unstructured.NestedString(item.Object, "status", "phase")

		if args.NodeName != "" && nodeName != args.NodeName {
			continue
		}

		receivers = append(receivers, map[string]interface{}{
			"name":      item.GetName(),
			"flowID":    flowID,
			"nodeName":  nodeName,
			"provider":  provider,
			"status":    status,
		})
	}

	return map[string]interface{}{
		"receivers": receivers,
	}, nil
}

func (s *RegistryMCPServer) handleConnect(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var args struct {
		SenderID   string `json:"senderID"`
		ReceiverID string `json:"receiverID"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}

	return map[string]interface{}{
		"senderID":   args.SenderID,
		"receiverID": args.ReceiverID,
		"status":     "connecting",
	}, nil
}

func (s *RegistryMCPServer) handleDisconnect(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var args struct {
		ReceiverID string `json:"receiverID"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}

	return map[string]interface{}{
		"receiverID": args.ReceiverID,
		"status":     "disconnecting",
	}, nil
}

func main() {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	ctx := clilog.IntoContext(context.Background(), clilog.Log.WithName("zeus-mcp-registry"))

	var metricsAddr, probeAddr string
	var nmosBaseURL string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "")
	flag.StringVar(&nmosBaseURL, "nmos-registry-url", "", "NMOS IS-04 registry base URL")
	flag.Parse()

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(zeusv1.AddToScheme(scheme))

	c, err := client.New(ctrl.GetConfigOrDie(), client.Options{Scheme: scheme})
	if err != nil {
		clilog.FromContext(ctx).Error(err, "unable to create client")
		os.Exit(1)
	}

	server := NewRegistryMCPServer(c, scheme, nmosBaseURL)

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := server.Run(ctx); err != nil {
		clilog.FromContext(ctx).Error(err, "server exited")
		os.Exit(1)
	}
}
