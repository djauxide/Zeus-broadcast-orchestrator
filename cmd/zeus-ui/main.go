package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
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

// uiServer serves the Zeus web dashboard and proxies Kubernetes API calls.
type uiServer struct {
	client client.Client
	scheme *runtime.Scheme
	addr   string
}

func newUIServer(c client.Client, scheme *runtime.Scheme, addr string) *uiServer {
	return &uiServer{
		client: c,
		scheme: scheme,
		addr:   addr,
	}
}

func (s *uiServer) Routes() *http.ServeMux {
	mux := http.NewServeMux()

	// Static files
	mux.Handle("/", http.FileServer(http.FS(staticFiles)))

	// API endpoints
	mux.HandleFunc("/api/zeusrunbooks", s.handleListRunbooks)
	mux.HandleFunc("/api/zeusrunbook/", s.handleGetRunbook)
	mux.HandleFunc("/api/naps", s.handleListNAPs)
	mux.HandleFunc("/api/naps/", s.handleGetNAP)
	mux.HandleFunc("/api/napsregistry", s.handleListNAPRegistry)
	mux.HandleFunc("/api/napsregistry/", s.handleGetNAPRegistry)
	mux.HandleFunc("/api/metrics", s.handleMetrics)
	mux.HandleFunc("/api/health", s.handleHealth)

	return mux
}

func (s *uiServer) handleListRunbooks(w http.ResponseWriter, r *http.Request) {
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
			"conditions": rb.Status.Conditions,
			"pathHealth": rb.Status.PathHealth,
			"creationTimestamp": rb.GetCreationTimestamp().Format(time.RFC3339),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"runbooks": items,
		"count":    len(items),
	})
}

func (s *uiServer) handleGetRunbook(w http.ResponseWriter, r *http.Request) {
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

func (s *uiServer) handleListNAPs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var list zeusv1.ZeusApplicationPackageList
	if err := s.client.List(ctx, &list); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	items := make([]map[string]interface{}, 0, len(list.Items))
	for _, nap := range list.Items {
		items = append(items, map[string]interface{}{
			"name":      nap.GetName(),
			"namespace": nap.GetNamespace(),
			"phase":     nap.Status.Phase,
			"image":     nap.Spec.Image,
			"observedGen": nap.Status.ObservedGen,
			"creationTimestamp": nap.GetCreationTimestamp().Format(time.RFC3339),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"naps": items,
		"count": len(items),
	})
}

func (s *uiServer) handleGetNAP(w http.ResponseWriter, r *http.Request) {
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

func (s *uiServer) handleListNAPRegistry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var list zeusv1.NAPRegistryList
	if err := s.client.List(ctx, &list); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	items := make([]map[string]interface{}, 0, len(list.Items))
	for _, reg := range list.Items {
		items = append(items, map[string]interface{}{
			"name":      reg.GetName(),
			"namespace": reg.GetNamespace(),
			"phase":     reg.Status.Phase,
			"napName":   reg.Spec.NAPName,
			"napVersion": reg.Spec.NAPVersion,
			"napType":   reg.Spec.NAPType,
			"submitter": reg.Spec.Submitter,
			"creationTimestamp": reg.GetCreationTimestamp().Format(time.RFC3339),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"registries": items,
		"count":      len(items),
	})
}

func (s *uiServer) handleGetNAPRegistry(w http.ResponseWriter, r *http.Request) {
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

func (s *uiServer) handleMetrics(w http.ResponseWriter, r *http.Request) {
	// Proxy metrics from the zeus-kernel metrics endpoint
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("# Zeus metrics proxy\n# Access /metrics directly on the zeus-kernel service\n"))
}

func (s *uiServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"healthy"}`))
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

	server := newUIServer(c, scheme, addr)
	mux := server.Routes()

	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		clilog.FromContext(ctx).Info("starting UI server", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			clilog.FromContext(ctx).Error(err, "server exited unexpectedly")
			os.Exit(1)
		}
	}()

	// Handle shutdown signals
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		clilog.FromContext(ctx).Error(err, "server shutdown error")
	}
}
