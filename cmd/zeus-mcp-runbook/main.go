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
	"fmt"
)

// RunbookMCPServer implements runbook lifecycle operations as MCP tools.
type RunbookMCPServer struct {
	client.Client
	scheme *runtime.Scheme
}

func NewRunbookMCPServer(c client.Client, scheme *runtime.Scheme) *RunbookMCPServer {
	return &RunbookMCPServer{
		Client: c,
		scheme: scheme,
	}
}

func (s *RunbookMCPServer) Run(ctx context.Context) error {
	srv := transport.NewServer(os.Stdin, os.Stdout)
	srv.WithLogger(log.New(os.Stderr, "[mcp-runbook] ", log.LstdFlags|log.Lmicroseconds))

	srv.Handle("initialize", s.handleInitialize)
	srv.Handle("tools/list", s.handleToolsList)
	srv.Handle("tools/call", s.handleToolsCall)
	srv.Handle("notifications/initialized", s.handleInitialized)
	srv.Handle("ping", s.handlePing)

	return srv.Start(ctx)
}

func (s *RunbookMCPServer) handleInitialize(ctx context.Context, params json.RawMessage) (interface{}, error) {
	return map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]interface{}{
			"tools": map[string]interface{}{},
		},
		"serverInfo": map[string]interface{}{
			"name":    "zeus-mcp-runbook",
			"version": "0.1.0",
		},
	}, nil
}

func (s *RunbookMCPServer) handleToolsList(ctx context.Context, params json.RawMessage) (interface{}, error) {
	tools := []map[string]interface{}{
		{
			"name":        "runbook_list",
			"description": "List ZeusRunbook resources",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"namespace": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "runbook_get",
			"description": "Get a ZeusRunbook by name",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":      map[string]interface{}{"type": "string"},
					"namespace": map[string]interface{}{"type": "string"},
				},
				"required": []string{"name", "namespace"},
			},
		},
		{
			"name":        "runbook_phase",
			"description": "Get the current phase of a ZeusRunbook",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":      map[string]interface{}{"type": "string"},
					"namespace": map[string]interface{}{"type": "string"},
				},
				"required": []string{"name", "namespace"},
			},
		},
	}
	return map[string]interface{}{
		"tools": tools,
	}, nil
}

func (s *RunbookMCPServer) handleToolsCall(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}

	switch call.Name {
	case "runbook_list":
		return s.handleListRunbooks(ctx, call.Arguments)
	case "runbook_get":
		return s.handleGetRunbook(ctx, call.Arguments)
	case "runbook_phase":
		return s.handleRunbookPhase(ctx, call.Arguments)
	default:
		return nil, fmt.Errorf("unknown tool: %s", call.Name)
	}
}

func (s *RunbookMCPServer) handleInitialized(ctx context.Context, params json.RawMessage) (interface{}, error) {
	return nil, nil
}

func (s *RunbookMCPServer) handlePing(ctx context.Context, params json.RawMessage) (interface{}, error) {
	return nil, nil
}

func (s *RunbookMCPServer) handleListRunbooks(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var args struct {
		Namespace string `json:"namespace,omitempty"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}

	var list zeusv1.ZeusRunbookList
	opts := []client.ListOption{}
	if args.Namespace != "" {
		opts = append(opts, client.InNamespace(args.Namespace))
	}

	if err := s.List(ctx, &list, opts...); err != nil {
		return nil, fmt.Errorf("list runbooks: %w", err)
	}

	runbooks := make([]map[string]interface{}, 0, len(list.Items))
	for _, rb := range list.Items {
		pathHealth := make([]map[string]interface{}, 0, len(rb.Status.PathHealth))
		for _, ph := range rb.Status.PathHealth {
			pathHealth = append(pathHealth, map[string]interface{}{
				"name":      ph.Name,
				"transport": ph.Transport,
				"state":     ph.State,
				"rttMs":     ph.RTTMs,
			})
		}

		runbooks = append(runbooks, map[string]interface{}{
			"name":       rb.GetName(),
			"namespace":  rb.GetNamespace(),
			"phase":      rb.Status.Phase,
			"eventClass": rb.Spec.EventClass,
			"conditions": rb.Status.Conditions,
			"pathHealth": pathHealth,
		})
	}

	return map[string]interface{}{
		"runbooks": runbooks,
	}, nil
}

func (s *RunbookMCPServer) handleGetRunbook(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var args struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}

	var rb zeusv1.ZeusRunbook
	if err := s.Get(ctx, client.ObjectKey{Namespace: args.Namespace, Name: args.Name}, &rb); err != nil {
		return nil, fmt.Errorf("get runbook: %w", err)
	}

	return map[string]interface{}{
		"name":       rb.GetName(),
		"namespace":  rb.GetNamespace(),
		"phase":      rb.Status.Phase,
		"eventClass": rb.Spec.EventClass,
		"naps":       rb.Spec.NAPs,
		"conditions": rb.Status.Conditions,
		"pathHealth": rb.Status.PathHealth,
	}, nil
}

func (s *RunbookMCPServer) handleRunbookPhase(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var args struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}

	var rb zeusv1.ZeusRunbook
	if err := s.Get(ctx, client.ObjectKey{Namespace: args.Namespace, Name: args.Name}, &rb); err != nil {
		return nil, fmt.Errorf("get runbook: %w", err)
	}

	reason := ""
	if len(rb.Status.Conditions) > 0 {
		reason = rb.Status.Conditions[0].Reason
	}

	return map[string]interface{}{
		"name":      rb.GetName(),
		"namespace": rb.GetNamespace(),
		"phase":     rb.Status.Phase,
		"reason":    reason,
	}, nil
}

func main() {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	ctx := clilog.IntoContext(context.Background(), clilog.Log.WithName("zeus-mcp-runbook"))

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

	server := NewRunbookMCPServer(c, scheme)

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := server.Run(ctx); err != nil {
		clilog.FromContext(ctx).Error(err, "server exited")
		os.Exit(1)
	}
}
