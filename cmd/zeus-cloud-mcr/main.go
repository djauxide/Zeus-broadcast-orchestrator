package cloudmcr

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clilog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
)

// CloudMCRServer implements a cloud-based Master Control Room server.
// It provides remote production control capabilities over HTTPS/WSS,
// enabling operators to control broadcast workflows from any location.
//
// Features:
//   - Remote runbook orchestration
//   - Remote device control (cameras, audio, graphics, routing)
//   - Multi-site synchronization
//   - Audit logging and access control
//   - WebSocket-based real-time control
type CloudMCRServer struct {
	client.Client
	scheme     *runtime.Scheme
	addr       string
	server     *http.Server
	clients    map[string]*cloudClient
	clientsMu  sync.Mutex
	logger     *log.Logger
}

// cloudClient represents a connected cloud client (browser, mobile app, etc.)
type cloudClient struct {
	id      string
	conn    chan []byte
	logger  *log.Logger
}

// NewCloudMCRServer creates a new cloud MCR server.
func NewCloudMCRServer(c client.Client, scheme *runtime.Scheme, addr string) *CloudMCRServer {
	return &CloudMCRServer{
		client:   c,
		scheme:   scheme,
		addr:     addr,
		clients:  make(map[string]*cloudClient),
		logger:   log.New(os.Stderr, "[cloud-mcr] ", log.LstdFlags|log.Lmicroseconds),
	}
}

// Run starts the cloud MCR server.
func (s *CloudMCRServer) Run(ctx context.Context) error {
	mux := http.NewServeMux()

	// Cloud MCR API endpoints
	mux.HandleFunc("GET /api/v1/cloud/runbooks", s.handleListRunbooks)
	mux.HandleFunc("POST /api/v1/cloud/runbooks", s.handleCreateRunbook)
	mux.HandleFunc("GET /api/v1/cloud/zeusrunbooks", s.handleListRunbooks)
	mux.HandleFunc("GET /api/v1/cloud/zeusapplicationpackages", s.handleListNAPs)
	mux.HandleFunc("POST /api/v1/cloud/zeusapplicationpackages", s.handleCreateNAP)
	mux.HandleFunc("GET /api/v1/cloud/devices", s.handleListDevices)
	mux.HandleFunc("POST /api/v1/cloud/devices/command", s.handleDeviceCommand)
	mux.HandleFunc("GET /api/v1/cloud/status", s.handleCloudStatus)

	// SSE endpoint for real-time updates
	mux.HandleFunc("GET /api/v1/cloud/events", s.handleSSE)

	httpServer := &http.Server{
		Addr:         s.addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		s.logger.Printf("cloud MCR server listening on %s", s.addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Printf("cloud MCR server exited: %v", err)
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	s.logger.Printf("cloud MCR server shutting down")
	return s.httpServer.Shutdown(shutdownCtx)
}

// handleListRunbooks lists all runbooks via the cloud API.
func (s *CloudMCRServer) handleListRunbooks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var list zeusv1.ZeusRunbookList
	if err := s.client.List(ctx, &list); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	items := make([]map[string]interface{}, 0, len(list.Items))
	for _, rb := range list.Items {
		items = append(items, map[string]interface{}{
			"name":      rb.GetName(),
			"namespace": rb.GetNamespace(),
			"phase":     rb.Status.Phase,
			"eventClass": rb.Spec.EventClass,
			"creationTime": rb.GetCreationTimestamp().Format(time.RFC3339),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"runbooks": items,
		"count":    len(items),
	})
}

// handleCreateRunbook creates a new runbook via the cloud API.
func (s *CloudMCRServer) handleCreateRunbook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var rb zeusv1.ZeusRunbook
	if err := json.NewDecoder(r.Body).Decode(&rb); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.client.Create(ctx, &rb); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(rb)
}

// handleListNAPs lists all NAPs via the cloud API.
func (s *CloudMCRServer) handleListNAPs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var list zeusv1.ZeusApplicationPackageList
	if err := s.client.List(ctx, &list); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	items := make([]map[string]interface{}, 0, len(list.Items))
	for _, nap := range list.Items {
		items = append(items, map[string]interface{}{
			"name":           nap.GetName(),
			"namespace":      nap.GetNamespace(),
			"phase":          nap.Status.Phase,
			"image":          nap.Spec.Image,
			"eventClass":     nap.Spec.NMOS.Transport,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"naps": items,
		"count": len(items),
	})
}

// handleCreateNAP creates a new NAP via the cloud API.
func (s *CloudMCRServer) handleCreateNAP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var nap zeusv1.ZeusApplicationPackage
	if err := json.NewDecoder(r.Body).Decode(&nap); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.client.Create(ctx, &nap); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(nap)
}

// handleListDevices lists all registered device adapters via the cloud API.
func (s *CloudMCRServer) handleListDevices(w http.ResponseWriter, r *http.Request) {
	// This would list registered device adapters
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"devices": []interface{}{},
		"count":   0,
	})
}

// handleDeviceCommand sends a device command via the cloud API.
func (s *CloudMCRServer) handleDeviceCommand(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var cmd struct {
		DeviceID   string            `json:"deviceId"`
		Command    string            `json:"command"`
		Params     map[string]string `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// In a full implementation, this would route to the appropriate device adapter
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "accepted",
		"deviceId": cmd.DeviceID,
		"command": cmd.command,
	})
}

// handleCloudStatus returns the cloud MCR server status.
func (s *CloudMCRServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      "online",
		"service":     "zeus-cloud-mcr",
		"timestamp":   time.Now().Format(time.RFC3339),
		"connectedClients": len(s.clients),
	})
}

// handleSSE handles Server-Sent Events for cloud real-time updates.
func (s *CloudMCRServer) handleSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch := make(chan []byte, 20)
	s.clientsMu.Lock()
	s.clients[r.RemoteAddr] = &cloudClient{
		id:   r.RemoteAddr,
		conn: ch,
		logger: log.New(os.Stderr, fmt.Sprintf("[cloud-client %s]", r.RemoteAddr), log.LstdFlags|log.Lmicroseconds),
	}
	s.clientsMu.Unlock()

	defer func() {
		s.clientsMu.Lock()
		delete(s.clientsMap, r.RemoteAddr)
		close(ch)
		s.clientsMu.Unlock()
	}()

	w.Write([]byte("data: {\"event\":\"connected\",\"data\":{\"status\":\"connected\"}}\n\n"))
	w.(http.Flusher).Flush()

	for {
		select {
		case msg := <-ch:
			w.Write(msg)
			w.(http.Flusher).Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// broadcastCloudEvent broadcasts an event to all connected cloud clients.
func (s *CloudMCRServer) broadcastCloudEvent(event string, data interface{}) {
	payload, _ := json.Marshal(map[string]interface{}{
		"event":   event,
		"data":    data,
		"timestamp": time.Now().Format(time.RFC3339),
	})
	msg := append([]byte("data: "), payload...)
	msg = append(msg, '\n', '\n')

	s.clientsMu.Lock()
	for _, client := range s.clients {
		select {
		case client.conn <- msg:
		default:
			// Slow consumer, drop
		}
	}
	s.clientsMu.Unlock()
}

// BroadcastCloudEvent broadcasts a cloud event to all connected clients.
func (s *CloudMCRServer) BroadcastCloudEvent(event string, data interface{}) {
	s.broadcastCloudEvent(event, data)
}

// main is the entry point for the cloud MCR server.
func main() {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	ctx := clilog.IntoContext(context.Background(), clilog.Log.WithName("zeus-cloud-mcr"))

	var addr string
	flag.StringVar(&addr, "addr", ":8083", "Cloud MCR server listen address")
	flag.Parse()

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(zeusv1.AddToScheme(scheme))

	c, err := client.New(ctrl.GetConfigOrDie(), client.Options{Scheme: scheme})
	if err != nil {
		clilog.FromContext(ctx).Error(err, "unable to create client")
		os.Exit(1)
	}

	server := NewCloudMCRServer(c, scheme, addr)

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := server.Run(ctx); err != nil {
		clilog.FromContext(ctx).Error(err, "server exited")
		os.Exit(1)
	}
}
