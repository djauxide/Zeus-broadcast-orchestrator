package main

import (
	"context"
	"flag"
	"net"
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

// SchedulerProbe probes network latency between sites and computes
// placement cost models for multi-site scheduling.
type SchedulerProbe struct {
	client.Client
	scheme *runtime.Scheme
	sites  []string
}

func NewSchedulerProbe(c client.Client, scheme *runtime.Scheme, sites []string) *SchedulerProbe {
	return &SchedulerProbe{
		Client: c,
		scheme: scheme,
		sites:  sites,
	}
}

// Run starts the scheduler probe, periodically probing site latency.
func (s *SchedulerProbe) Run(ctx context.Context) error {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Probe immediately on start.
	s.probeSites(ctx)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.probeSites(ctx)
		}
	}
}

// probeSites measures RTT to each configured site and logs the results.
func (s *SchedulerProbe) probeSites(ctx context.Context) {
	for _, site := range s.sites {
		rtt, err := probeRTT(site, 5*time.Second)
		if err != nil {
			clilog.FromContext(ctx).Error(err, "site probe failed", "site", site)
			continue
		}
		clilog.FromContext(ctx).Info("site latency", "site", site, "rttMs", rtt.Milliseconds())
	}
}

// probeRTT measures round-trip time to a host:port using TCP.
func probeRTT(address string, timeout time.Duration) (time.Duration, error) {
	d := net.Dialer{Timeout: timeout}
	start := time.Now()
	conn, err := d.DialContext(context.Background(), "tcp", address)
	if err != nil {
		return 0, err
	}
	conn.Close()
	return time.Since(start), nil
}

func main() {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	ctx := clilog.IntoContext(context.Background(), clilog.Log.WithName("zeus-scheduler-probe"))

	var probeInterval string
	var sites string
	flag.StringVar(&probeInterval, "probe-interval", "30s", "Latency probe interval")
	flag.StringVar(&sites, "sites", "", "Comma-separated list of site host:port pairs to probe")
	flag.Parse()

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(zeusv1.AddToScheme(scheme))

	c, err := client.New(ctrl.GetConfigOrDie(), client.Options{Scheme: scheme})
	if err != nil {
		clilog.FromContext(ctx).Error(err, "unable to create client")
		os.Exit(1)
	}

	siteList := []string{}
	if sites != "" {
		for _, s := range splitSites(sites) {
			siteList = append(siteList, s)
		}
	}

	probe := NewSchedulerProbe(c, scheme, siteList)

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := probe.Run(ctx); err != nil {
		clilog.FromContext(ctx).Error(err, "server exited")
		os.Exit(1)
	}
}

func splitSites(s string) []string {
	var result []string
	for _, part := range splitComma(s) {
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func splitComma(s string) []string {
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	result = append(result, s[start:])
	return result
}
