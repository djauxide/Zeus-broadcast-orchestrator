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

//go:embed static/*
var staticFiles embed.FS

// UIConfig holds configuration for the web UI server.
type UIConfig struct {
	Addr         string
	MetricsAddr  string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

// Server is the Zeus web UI server.
type Server struct {
	httpServer *http.Server
	client     client.Client
	scheme     *runtime.Scheme
	config     UIConfig
	logger     *log.Logger
}

// NewServer creates a new Zeus web UI server.
func NewServer(c client.Client, scheme *runtime.Scheme, cfg UIConfig) *Server {
	return &Server{
		client: c,
		scheme: scheme,
		config: cfg,
		logger: log.New(os.Stderr, "[zeus-ui] ", log.LstdFlags|log.Lmicroseconds),
	}
}

// Routes returns the HTTP mux with all routes registered.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// Static file server
	staticHandler := http.FileServer(http.FS(staticFiles))
	mux.Handle("GET /static/", http.StripPrefix("/static/", staticHandler))
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
	mux.HandleFunc("GET /api/v1/stats", s.handleStats)
	mux.HandleFunc("GET /api/v1/ws", s.handleWebSocket)

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

	go func() {
		s.logger.Printf("starting UI server on %s", s.config.Addr)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Printf("server exited unexpectedly: %v", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	s.logger.Printf("shutting down UI server")
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
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

// handleWebSocket handles WebSocket connections for real-time updates.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("WebSocket endpoint - use WebSocket client"))
}

func main() {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	ctx := clilog.IntoContext(context.Background(), clilog.Log.WithName("zeus-ui"))

	var addr string
	var metricsAddr string
	flag.StringVar(&addr, "addr", ":8082", "UI server listen address")
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8083", "UI metrics bind address")
	flag.Parse()

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(zeusv1.AddToScheme(scheme))

	c, err := client.New(ctrl.GetConfigOrDie(), client.Options{Scheme: scheme})
	if err != nil {
		clilog.FromContext(ctx).Error(err, "unable to create client")
		os.Exit(1)
	}

	server := NewServer(c, scheme, UIConfig{
		Addr:         addr,
		MetricsAddr:  metricsAddr,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	})

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := server.Run(ctx); err != nil {
		clilog.FromContext(ctx).Error(err, "server exited")
		os.Exit(1)
	}
}
