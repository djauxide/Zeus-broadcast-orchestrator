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
	"github.com/zeus-broadcast/zeus-kernel/internal/fabric"
	"github.com/zeus-broadcast/zeus-kernel/internal/mcp/transport"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"fmt"
)

// FabricMCPServer implements fabric operations as MCP tools.
type FabricMCPServer struct {
	client.Client
	scheme *runtime.Scheme
}

func NewFabricMCPServer(c client.Client, scheme *runtime.Scheme) *FabricMCPServer {
	return &FabricMCPServer{
		Client: c,
		scheme: scheme,
	}
}

func (s *FabricMCPServer) Run(ctx context.Context) error {
	srv := transport.NewServer(os.Stdin, os.Stdout)
	srv.WithLogger(log.New(os.Stderr, "[mcp-fabric] ", log.LstdFlags|log.Lmicroseconds))

	srv.Handle("initialize", s.handleInitialize)
	srv.Handle("tools/list", s.handleToolsList)
	srv.Handle("tools/call", s.handleToolsCall)
	srv.Handle("notifications/initialized", s.handleInitialized)
	srv.Handle("ping", s.handlePing)

	return srv.Start(ctx)
}

func (s *FabricMCPServer) handleInitialize(ctx context.Context, params json.RawMessage) (interface{}, error) {
	return map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]interface{}{
			"tools": map[string]interface{}{},
		},
		"serverInfo": map[string]interface{}{
			"name":    "zeus-mcp-fabric",
			"version": "0.1.0",
		},
	}, nil
}

func (s *FabricMCPServer) handleToolsList(ctx context.Context, params json.RawMessage) (interface{}, error) {
	tools := []map[string]interface{}{
		{
			"name":        "fabric_ensure_receiver",
			"description": "Ensure an MxlReceiver exists for a runbook NAP",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"runbook":  map[string]interface{}{"type": "string"},
					"napName":  map[string]interface{}{"type": "string"},
					"flowID":   map[string]interface{}{"type": "string"},
					"podName":  map[string]interface{}{"type": "string"},
					"provider": map[string]interface{}{"type": "string"},
				},
				"required": []string{"runbook", "napName", "flowID", "podName", "provider"},
			},
		},
		{
			"name":        "fabric_delete_receiver",
			"description": "Delete an MxlReceiver",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"napName": map[string]interface{}{"type": "string"},
				},
				"required": []string{"napName"},
			},
		},
		{
			"name":        "fabric_list_receivers",
			"description": "List MxlReceiver resources",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"namespace": map[string]interface{}{"type": "string"},
				},
			},
		},
	}
	return map[string]interface{}{
		"tools": tools,
	}, nil
}

func (s *FabricMCPServer) handleToolsCall(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}

	switch call.Name {
	case "fabric_ensure_receiver":
		return s.handleEnsureReceiver(ctx, call.Arguments)
	case "fabric_delete_receiver":
		return s.handleDeleteReceiver(ctx, call.Arguments)
	case "fabric_list_receivers":
		return s.handleListReceivers(ctx, call.Arguments)
	default:
		return nil, fmt.Errorf("unknown tool: %s", call.Name)
	}
}

func (s *FabricMCPServer) handleInitialized(ctx context.Context, params json.RawMessage) (interface{}, error) {
	return nil, nil
}

func (s *FabricMCPServer) handlePing(ctx context.Context, params json.RawMessage) (interface{}, error) {
	return nil, nil
}

func (s *FabricMCPServer) handleEnsureReceiver(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var args struct {
		Runbook  string `json:"runbook"`
		NapName  string `json:"napName"`
		FlowID   string `json:"flowID"`
		PodName  string `json:"podName"`
		Provider string `json:"provider"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}

	var rb zeusv1.ZeusRunbook
	if err := s.Get(ctx, client.ObjectKey{Namespace: "default", Name: args.Runbook}, &rb); err != nil {
		return nil, fmt.Errorf("get runbook: %w", err)
	}

	f := fabric.NewRealClient(s.Client)
	if err := f.EnsureReceiver(ctx, "default", args.NapName, args.FlowID, args.PodName, args.Provider, &rb); err != nil {
		return nil, fmt.Errorf("ensure receiver: %w", err)
	}

	return map[string]interface{}{
		"napName":  args.NapName,
		"flowID":   args.FlowID,
		"podName":  args.PodName,
		"provider": args.Provider,
		"status":   "ensured",
	}, nil
}

func (s *FabricMCPServer) handleDeleteReceiver(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var args struct {
		NapName string `json:"napName"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}

	f := fabric.NewRealClient(s.Client)
	if err := f.DeleteReceiver(ctx, "default", args.NapName); err != nil {
		return nil, fmt.Errorf("delete receiver: %w", err)
	}

	return map[string]interface{}{
		"napName": args.NapName,
		"status":  "deleted",
	}, nil
}

func (s *FabricMCPServer) handleListReceivers(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var args struct {
		Namespace string `json:"namespace,omitempty"`
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

	opts := []client.ListOption{}
	if args.Namespace != "" {
		opts = append(opts, client.InNamespace(args.Namespace))
	}

	if err := s.List(ctx, uv, opts...); err != nil {
		return nil, fmt.Errorf("list receivers: %w", err)
	}

	receivers := make([]map[string]interface{}, 0, len(uv.Items))
	for _, item := range uv.Items {
		flowID, _, _ := unstructured.NestedString(item.Object, "spec", "flowID")
		nodeName, _, _ := unstructured.NestedString(item.Object, "spec", "podRef", "name")
		provider, _, _ := unstructured.NestedString(item.Object, "spec", "provider")
		status, _, _ := unstructured.NestedString(item.Object, "status", "phase")

		receivers = append(receivers, map[string]interface{}{
			"name":      item.GetName(),
			"namespace": item.GetNamespace(),
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

func main() {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	ctx := clilog.IntoContext(context.Background(), clilog.Log.WithName("zeus-mcp-fabric"))

	var metricsAddr, probeAddr string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "")
	flag.Parse()

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(zeusv1.AddToScheme(scheme))

	c, err := client.New(ctrl.GetConfigOrDie(), client.Options{Scheme: scheme})
	if err != nil {
		clilog.FromContext(ctx).Error(err, "unable to create client")
		os.Exit(1)
	}

	server := NewFabricMCPServer(c, scheme)

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := server.Run(ctx); err != nil {
		clilog.FromContext(ctx).Error(err, "server exited")
		os.Exit(1)
	}
}
