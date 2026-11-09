package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	clilog "sigs.k8s.io/controller-runtime/pkg/log"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
)

//go:embed static/*
var staticFiles embed.FS

// Server is the Zeus Broadcast Orchestrator web UI server.
type Server struct {
	httpServer *http.Server
	client     client.Client
	scheme     *runtime.Scheme
	config     UIConfig
	logger     *log.Logger

	clientsMu sync.Mutex
	clients   map[chan []byte]bool
}

// UIConfig holds configuration for the web UI server.
type UIConfig struct {
	Addr         string
	MetricsAddr  string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

// NewServer creates a new Zeus web UI server.
func NewServer(c client.Client, scheme *runtime.Scheme, cfg UIConfig) *Server {
	return &Server{
		client:  c,
		scheme:  scheme,
		config:  cfg,
		clients: make(map[chan []byte]bool),
		logger:  log.New(os.Stderr, "[zeus-ui] ", log.LstdFlags|log.Lmicroseconds),
	}
}

// main is the entry point for the Zeus Broadcast Orchestrator web UI server.
func main() {
	addr := flag.String("addr", ":8082", "HTTP listen address")
	flag.Parse()

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		log.Fatalf("failed to add client-go scheme: %v", err)
	}
	if err := zeusv1.AddToScheme(scheme); err != nil {
		log.Fatalf("failed to add zeus scheme: %v", err)
	}

	cfg, err := config.GetConfig()
	if err != nil {
		log.Fatalf("failed to get k8s config: %v", err)
	}

	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		log.Fatalf("failed to create k8s client: %v", err)
	}

	srv := NewServer(c, scheme, UIConfig{
		Addr:         *addr,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := srv.Run(ctx); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// Routes returns the HTTP mux with all routes registered.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// Static file server
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFiles))))
	mux.HandleFunc("GET /", s.serveIndex)

	// API v1 routes
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/zeusrunbooks", s.handleListRunbooks)
	mux.HandleFunc("GET /api/v1/zeusrunbook/", s.handleGetRunbook)
	mux.HandleFunc("POST /api/v1/zeusrunbook", s.handleCreateRunbook)
	mux.HandleFunc("PUT /api/v1/zeusrunbook/", s.handleUpdateRunbook)
	mux.HandleFunc("DELETE /api/v1/zeusrunbook/", s.handleDeleteRunbook)
	mux.HandleFunc("GET /api/v1/zeusapplicationpackages", s.handleListNAPs)
	mux.HandleFunc("GET /api/v1/zeusapplicationpackage/", s.handleGetNAP)
	mux.HandleFunc("POST /api/v1/zeusapplicationpackage", s.handleCreateNAP)
	mux.HandleFunc("PUT /api/v1/zeusapplicationpackage/", s.handleUpdateNAP)
	mux.HandleFunc("DELETE /api/v1/zeusapplicationpackage/", s.handleDeleteNAP)
	mux.HandleFunc("GET /api/v1/napregistry", s.handleListNAPRegistry)
	mux.HandleFunc("GET /api/v1/napregistry/", s.handleGetNAPRegistry)
	mux.HandleFunc("POST /api/v1/napregistry", s.handleCreateNAPRegistry)
	mux.HandleFunc("PUT /api/v1/napregistry/", s.handleUpdateNAPRegistry)
	mux.HandleFunc("DELETE /api/v1/napregistry/", s.handleDeleteNAPRegistry)
	mux.HandleFunc("GET /api/v1/devices", s.handleListDevices)
	mux.HandleFunc("GET /api/v1/devices/", s.handleGetDevice)
	mux.HandleFunc("POST /api/v1/devices/", s.handleDeviceCommand)
	mux.HandleFunc("GET /api/v1/stats", s.handleStats)
	mux.HandleFunc("GET /api/v1/events", s.handleSSE)

	// Broadcast tooling sections
	mux.HandleFunc("GET /api/v1/routing", s.handleRouting)
	mux.HandleFunc("GET /api/v1/visionmixer", s.handleVisionMixer)
	mux.HandleFunc("GET /api/v1/servo", s.handleServo)
	mux.HandleFunc("GET /api/v1/shading", s.handleShading)
	mux.HandleFunc("GET /api/v1/monitoring", s.handleMonitoring)
	mux.HandleFunc("GET /api/v1/broadcast-tools", s.handleBroadcastTools)

	return mux
}

// Run starts the HTTP server and blocks until shutdown.
func (s *Server) Run(ctx context.Context) error {
	mux := s.Routes()

	handler := s.withLogging(s.withRecovery(mux))

	s.httpServer = &http.Server{
		Addr:         s.config.Addr,
		Handler:      handler,
		ReadTimeout:  s.config.ReadTimeout,
		WriteTimeout: s.config.WriteTimeout,
		IdleTimeout:  s.config.IdleTimeout,
	}

	go s.BroadcastStats(ctx)

	go func() {
		s.logger.Printf("Zeus UI server starting on %s", s.config.Addr)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Printf("UI server exited unexpectedly: %v", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	s.logger.Printf("Zeus UI server shutting down")
	if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
		clilog.FromContext(ctx).Error(err, "server shutdown error")
		return err
	}

	return nil
}

func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		s.logger.Printf("%s %s %v", r.Method, r.URL.Path, time.Since(start))
	})
}

func (s *Server) withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				clilog.FromContext(r.Context()).Error(fmt.Errorf("panic recovered: %v", rec), "panic recovered")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	data, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.Write(data)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "healthy",
		"service":   "zeus-ui",
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

// ===== Runbook CRUD =====

func (s *Server) handleListRunbooks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var list zeusv1.ZeusRunbookList
	if err := s.client.List(ctx, &list); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	items := make([]map[string]interface{}, 0, len(list.Items))
	for _, rb := range list.Items {
		items = append(items, map[string]interface{}{
			"name":           rb.GetName(),
			"namespace":      rb.GetNamespace(),
			"phase":          rb.Status.Phase,
			"eventClass":     rb.Spec.EventClass,
			"creationTime":   rb.GetCreationTimestamp().Format(time.RFC3339),
			"conditions":     rb.Status.Conditions,
			"pathHealth":     rb.Status.PathHealth,
			"activeNAPs":     rb.Status.ActiveNAPs,
			"suggestedAction": rb.Status.SuggestedAction,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"runbooks": items,
		"count":    len(items),
	})
}

func (s *Server) handleGetRunbook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := r.PathValue("name")
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = "default"
	}

	var rb zeusv1.ZeusRunbook
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &rb); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rb)
}

func (s *Server) handleCreateRunbook(w http.ResponseWriter, r *http.Request) {
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

func (s *Server) handleUpdateRunbook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := r.PathValue("name")
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = "default"
	}

	var rb zeusv1.ZeusRunbook
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &rb); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	var update zeusv1.ZeusRunbook
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	rb.Spec = update.Spec
	rb.Status = update.Status

	if err := s.client.Update(ctx, &rb); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rb)
}

func (s *Server) handleDeleteRunbook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := r.PathValue("name")
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = "default"
	}

	var rb zeusv1.ZeusRunbook
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &rb); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	if err := s.client.Delete(ctx, &rb); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNoContent)
}

// ===== NAP CRUD =====

func (s *Server) handleListNAPs(w http.ResponseWriter, r *http.Request) {
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
			"imageSignature": nap.Spec.ImageSignature,
			"observedGen":    nap.Status.ObservedGen,
			"cpu":            nap.Spec.CRM.CPU,
			"memory":         nap.Spec.CRM.Memory,
			"domainRef":      nap.Spec.CRM.MediaIO.DomainRef,
			"transport":      nap.Spec.NMOS.Transport,
			"creationTime":   nap.GetCreationTimestamp().Format(time.RFC3339),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"naps": items,
		"count": len(items),
	})
}

func (s *Server) handleGetNAP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := r.PathValue("name")
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = "default"
	}

	var nap zeusv1.ZeusApplicationPackage
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &nap); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(nap)
}

func (s *Server) handleCreateNAP(w http.ResponseWriter, r *http.Request) {
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

func (s *Server) handleUpdateNAP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := r.PathValue("name")
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = "default"
	}

	var nap zeusv1.ZeusApplicationPackage
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &nap); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	var update zeusv1.ZeusApplicationPackage
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	nap.Spec = update.Spec
	nap.Status = update.Status

	if err := s.client.Update(ctx, &nap); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(nap)
}

func (s *Server) handleDeleteNAP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := r.PathValue("name")
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = "default"
	}

	var nap zeusv1.ZeusApplicationPackage
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &nap); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	if err := s.client.Delete(ctx, &nap); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNoContent)
}

// ===== NAP Registry CRUD =====

func (s *Server) handleListNAPRegistry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var list zeusv1.NAPRegistryList
	if err := s.client.List(ctx, &list); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	items := make([]map[string]interface{}, 0, len(list.Items))
	for _, reg := range list.Items {
		items = append(items, map[string]interface{}{
			"name":          reg.GetName(),
			"namespace":     reg.GetNamespace(),
			"phase":         reg.Status.Phase,
			"napName":       reg.Spec.NAPName,
			"napVersion":    reg.Spec.NAPVersion,
			"napType":       reg.Spec.NAPType,
			"submitter":     reg.Spec.Submitter,
			"creationTime":  reg.GetCreationTimestamp().Format(time.RFC3339),
			"approvedAt":    reg.Status.ApprovedAt,
			"rejectedAt":    reg.Status.RejectedAt,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"registries": items,
		"count":      len(items),
	})
}

func (s *Server) handleGetNAPRegistry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := r.PathValue("name")
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = "default"
	}

	var reg zeusv1.NAPRegistry
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &reg); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reg)
}

func (s *Server) handleCreateNAPRegistry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var reg zeusv1.NAPRegistry
	if err := json.NewDecoder(r.Body).Decode(&reg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.client.Create(ctx, &reg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(reg)
}

func (s *Server) handleUpdateNAPRegistry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := r.PathValue("name")
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = "default"
	}

	var reg zeusv1.NAPRegistry
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &reg); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	var update zeusv1.NAPRegistry
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	reg.Spec = update.Spec
	reg.Status = update.Status

	if err := s.client.Update(ctx, &reg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reg)
}

func (s *Server) handleDeleteNAPRegistry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := r.PathValue("name")
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = "default"
	}

	var reg zeusv1.NAPRegistry
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &reg); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	if err := s.client.Delete(ctx, &reg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNoContent)
}

// ===== Device Control =====

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var runbooks zeusv1.ZeusRunbookList
	if err := s.client.List(ctx, &runbooks); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	devices := make([]map[string]interface{}, 0)
	for _, rb := range runbooks.Items {
		for _, nap := range rb.Spec.NAPs {
			devices = append(devices, map[string]interface{}{
				"id":   nap.NAP,
				"name": nap.Name,
				"type": "device",
				"status": "online",
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"devices": devices,
		"count":   len(devices),
	})
}

func (s *Server) handleGetDevice(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	device := map[string]interface{}{
		"id":      name,
		"name":    name,
		"type":    "device",
		"status":  "online",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(device)
}

func (s *Server) handleDeviceCommand(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Command string            `json:"command"`
		Params  map[string]string `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "sent",
		"command": req.Command,
		"result":  "ok",
	})
}

// ===== Stats =====

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var runbooks zeusv1.ZeusRunbookList
	if err := s.client.List(ctx, &runbooks); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var naps zeusv1.ZeusApplicationPackageList
	if err := s.client.List(ctx, &naps); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var registry zeusv1.NAPRegistryList
	if err := s.client.List(ctx, &registry); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	phaseCounts := make(map[string]int)
	for _, rb := range runbooks.Items {
		phaseCounts[string(rb.Status.Phase)]++
	}

	napPhaseCounts := make(map[string]int)
	for _, nap := range naps.Items {
		napPhaseCounts[string(nap.Status.Phase)]++
	}

	registryPhaseCounts := make(map[string]int)
	for _, reg := range registry.Items {
		registryPhaseCounts[string(reg.Status.Phase)]++
	}

	// Network protocol health
	networkProtocols := map[string]interface{}{
		"bgp": map[string]interface{}{
			"state":      "up",
			"sessions":   2,
			"disruptions": 0,
		},
		"igmp": map[string]interface{}{
			"state":   "up",
			"groups":  4,
		},
		"srt": map[string]interface{}{
			"state":   "up",
			"paths":   2,
			"rttMs":   45,
		},
		"rist": map[string]interface{}{
			"state":   "up",
			"paths":   2,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"runbooks": map[string]interface{}{
			"total":  len(runbooks.Items),
			"phases": phaseCounts,
		},
		"naps": map[string]interface{}{
			"total":  len(naps.Items),
			"phases": napPhaseCounts,
		},
		"registry": map[string]interface{}{
			"total":  len(registry.Items),
			"phases": registryPhaseCounts,
		},
		"networkProtocols": networkProtocols,
		"timestamp":      time.Now().Format(time.RFC3339),
	})
}

// handleWebSocket handles WebSocket connections for real-time updates.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("WebSocket endpoint - use SSE instead"))
}

// handleSSE handles SSE connections for real-time updates.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch := make(chan []byte, 20)
	s.clientsMu.Lock()
	s.clients[ch] = true
	s.clientsMu.Unlock()

	defer func() {
		s.clientsMu.Lock()
		delete(s.clients, ch)
		close(ch)
		s.clientsMu.Unlock()
	}()

	initMsg := "data: {\"event\":\"connected\",\"data\":{\"status\":\"connected\"}}\n\n"
	w.Write([]byte(initMsg))
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

// broadcastEvent broadcasts an event to all connected SSE clients.
func (s *Server) broadcastEvent(event string, data interface{}) {
	payload, _ := json.Marshal(map[string]interface{}{
		"event":   event,
		"data":    data,
		"timestamp": time.Now().Format(time.RFC3339),
	})
	msg := append([]byte("data: "), payload...)
	msg = append(msg, '\n', '\n')

	s.clientsMu.Lock()
	for ch := range s.clients {
		select {
		case ch <- msg:
		default:
			// Slow consumer, drop
			}
		}
	s.clientsMu.Unlock()
}

// BroadcastStats broadcasts stats to all connected SSE clients.
func (s *Server) BroadcastStats(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var runbooks zeusv1.ZeusRunbookList
			if err := s.client.List(ctx, &runbooks); err != nil {
				continue
			}
			var naps zeusv1.ZeusApplicationPackageList
			if err := s.client.List(ctx, &naps); err != nil {
				continue
			}
			var registry zeusv1.NAPRegistryList
			if err := s.client.List(ctx, &registry); err != nil {
				continue
			}

			phaseCounts := make(map[string]int)
			for _, rb := range runbooks.Items {
				phaseCounts[string(rb.Status.Phase)]++
			}
			napPhaseCounts := make(map[string]int)
			for _, nap := range naps.Items {
				napPhaseCounts[string(nap.Status.Phase)]++
			}
			registryPhaseCounts := make(map[string]int)
			for _, reg := range registry.Items {
				registryPhaseCounts[string(reg.Status.Phase)]++
			}

			stats := map[string]interface{}{
				"runbooks": map[string]interface{}{
					"total":  len(runbooks.Items),
					"phases": phaseCounts,
				},
				"naps": map[string]interface{}{
					"total":  len(naps.Items),
					"phases": napPhaseCounts,
				},
				"registry": map[string]interface{}{
					"total":  len(registry.Items),
					"phases": registryPhaseCounts,
				},
				"timestamp": time.Now().Format(time.RFC3339),
			}

			s.broadcastEvent("stats", stats)
		}
	}
}

// ===== Broadcast Tooling Handlers =====

func (s *Server) handleRouting(w http.ResponseWriter, r *http.Request) {
	routers := []map[string]interface{}{
		{
			"id":          "swp08-1",
			"name":        "SW-P-08 Router A",
			"type":        "SW-P-08",
			"status":      "online",
			"inputs":      64,
			"outputs":     32,
			"crosspoints": 2048,
		},
		{
			"id":          "ember-1",
			"name":        "Ember+ Router B",
			"type":        "Ember+",
			"status":      "online",
			"inputs":      48,
			"outputs":     24,
			"crosspoints": 1152,
		},
	}
	writeJSON(w, map[string]interface{}{
		"routers": routers,
		"count":   len(routers),
	})
}

func (s *Server) handleVisionMixer(w http.ResponseWriter, r *http.Request) {
	mixers := []map[string]interface{}{
		{
			"id":        "vm-1",
			"name":      "Vision Mixer A",
			"program":   "CAM-1",
			"preview":   "CAM-2",
			"transition": "mix",
			"meCount":   2,
			"status":    "online",
		},
		{
			"id":        "vm-2",
			"name":      "Vision Mixer B",
			"program":   "GFX-1",
			"preview":   "CAM-3",
			"transition": "cut",
			"meCount":   1,
			"status":    "online",
		},
	}
	writeJSON(w, map[string]interface{}{
		"mixers": mixers,
		"count":  len(mixers),
	})
}

func (s *Server) handleServo(w http.ResponseWriter, r *http.Request) {
	servos := []map[string]interface{}{
		{
			"id":      "servo-1",
			"name":    "Camera Pan-Tilt 1",
			"type":    "pan-tilt",
			"position": "45°",
			"target":  "45°",
			"speed":   "12",
			"status":  "online",
		},
		{
			"id":      "servo-2",
			"name":    "Camera Zoom 1",
			"type":    "zoom",
			"position": "0%",
			"target":  "0%",
			"speed":   "8",
			"status":  "online",
		},
	}
	writeJSON(w, map[string]interface{}{
		"servos": servos,
		"count":  len(servos),
	})
}

func (s *Server) handleShading(w http.ResponseWriter, r *http.Request) {
	multiviewers := []map[string]interface{}{
		{
			"id":          "mv-1",
			"name":        "Multiviewer A",
			"layout":      "4x2",
			"sources":     8,
			"tallies":     4,
			"safeZones":   true,
			"status":      "online",
		},
		{
			"id":          "mv-2",
			"name":        "Multiviewer B",
			"layout":      "2x2",
			"sources":     4,
			"tallies":     2,
			"safeZones":   true,
			"status":      "online",
		},
	}
	writeJSON(w, map[string]interface{}{
		"multiviewers": multiviewers,
		"count":        len(multiviewers),
	})
}

func (s *Server) handleMonitoring(w http.ResponseWriter, r *http.Request) {
	monitors := []map[string]interface{}{
		{
			"id":          "mon-1",
			"name":        "Program Monitor",
			"source":      "PGM",
			"loudness":    -23.4,
			"peak":        -6.2,
			"videoStatus": "ok",
			"audioStatus": "ok",
		},
		{
			"id":          "mon-2",
			"name":        "Preview Monitor",
			"source":      "PVW",
			"loudness":    -24.1,
			"peak":        -8.1,
			"videoStatus": "ok",
			"audioStatus": "ok",
		},
	}
	writeJSON(w, map[string]interface{}{
		"monitors": monitors,
		"count":    len(monitors),
	})
}

func (s *Server) handleBroadcastTools(w http.ResponseWriter, r *http.Request) {
	tools := []map[string]interface{}{
		{
			"id":       "tool-1",
			"name":     "NAP Conformance Check",
			"category": "conformance",
			"status":   "idle",
			"lastRun":  "—",
			"result":   "—",
		},
		{
			"id":       "tool-2",
			"name":     "Runbook Validator",
			"category": "validation",
			"status":   "idle",
			"lastRun":  "—",
			"result":   "—",
		},
		{
			"id":       "tool-3",
			"name":     "NMOS IS-04 Discovery",
			"category": "discovery",
			"status":   "idle",
			"lastRun":  "—",
			"result":   "—",
		},
	}
	writeJSON(w, map[string]interface{}{
		"tools": tools,
		"count": len(tools),
	})
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}