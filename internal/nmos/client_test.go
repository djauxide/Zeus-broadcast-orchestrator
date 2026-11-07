package nmos

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestClientFindSender(t *testing.T) {
	// Test server that returns a list of resources with one sender
	resources := []Resource{
		{ID: "receiver-1", Label: "Receiver 1", Type: "receiver"},
		{ID: "sender-1", Label: "Camera 1", Type: "sender",
			Manifest: Manifest{MediaType: "video/raw", Essence: "raw", Sampling: "YCbCr:4:2:2:10bit"}},
		{ID: "sender-2", Label: "Camera 2", Type: "sender",
			Manifest: Manifest{MediaType: "video/raw", Essence: "raw", Sampling: "YCbCr:4:2:2:12bit"}},
	}

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"test-token","expires_in":3600}`))
	}))
	defer tokenSrv.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/x-nmos/query/v1.4/resources" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "missing or invalid token", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resources)
	}))
	defer srv.Close()

	client := &Client{
		BaseURL:   srv.URL,
		TokenURL:  tokenSrv.URL + "/token",
		HTTP:      &http.Client{Timeout: 5 * time.Second},
	}
	ctx := context.Background()

	// Test exact match
	sender, err := client.FindSender(ctx, SenderMatch{
		MediaType: "video/raw",
		Essence:   "raw",
		Sampling:  "YCbCr:4:2:2:10bit",
	})
	if err != nil {
		t.Fatalf("FindSender: %v", err)
	}
	if sender.ID != "sender-1" {
		t.Fatalf("expected sender-1, got %s", sender.ID)
	}

	// Test partial match (only media type)
	sender, err = client.FindSender(ctx, SenderMatch{MediaType: "video/raw"})
	if err != nil {
		t.Fatalf("FindSender partial: %v", err)
	}
	if sender.ID != "sender-1" {
		t.Fatalf("expected sender-1 (first sorted match), got %s", sender.ID)
	}

	// Test no match
	_, err = client.FindSender(ctx, SenderMatch{MediaType: "audio/raw"})
	if err == nil {
		t.Fatal("expected error for no matching sender")
	}
}

func TestClientFindSenderEmptyResults(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"test-token","expires_in":3600}`))
	}))
	defer tokenSrv.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]Resource{})
	}))
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, TokenURL: tokenSrv.URL + "/token", HTTP: &http.Client{Timeout: 5 * time.Second}}
	ctx := context.Background()

	_, err := client.FindSender(ctx, SenderMatch{MediaType: "video/raw"})
	if err == nil {
		t.Fatal("expected error for empty results")
	}
}

func TestClientFindSenderServerError(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"test-token","expires_in":3600}`))
	}))
	defer tokenSrv.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, TokenURL: tokenSrv.URL + "/token", HTTP: &http.Client{Timeout: 5 * time.Second}}
	ctx := context.Background()

	_, err := client.FindSender(ctx, SenderMatch{MediaType: "video/raw"})
	if err == nil {
		t.Fatal("expected error for server error")
	}
}

func TestSenderMatchDeterministicOrdering(t *testing.T) {
	// Two senders that both match - should return the one with lower label
	resources := []Resource{
		{ID: "sender-z", Label: "Zebra Sender", Type: "sender",
			Manifest: Manifest{MediaType: "video/raw", Essence: "raw", Sampling: "YCbCr:4:2:2:10bit"}},
		{ID: "sender-a", Label: "Alpha Sender", Type: "sender",
			Manifest: Manifest{MediaType: "video/raw", Essence: "raw", Sampling: "YCbCr:4:2:2:10bit"}},
	}

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"test-token","expires_in":3600}`))
	}))
	defer tokenSrv.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resources)
	}))
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, TokenURL: tokenSrv.URL + "/token", HTTP: &http.Client{Timeout: 5 * time.Second}}
	ctx := context.Background()

	sender, err := client.FindSender(ctx, SenderMatch{MediaType: "video/raw"})
	if err != nil {
		t.Fatalf("FindSender: %v", err)
	}
	if sender.ID != "sender-a" {
		t.Fatalf("expected sender-a (Alpha Sender sorts first), got %s", sender.ID)
	}
}

func TestNodeUnmarshal(t *testing.T) {
	jsonStr := `{
		"id": "node-1",
		"label": "Test Node",
		"version": "1.0.0",
		"clock": "PTP",
		"hostname": "test-host",
		"interfaces": [
			{"id": "int-1", "node_id": "node-1", "ip": [{"address": "10.0.0.1", "family": "ipv4"}]}
		],
		"services": [
			{"id": "svc-1", "receiver": true, "sender": false}
		]
	}`
	var node Node
	if err := json.Unmarshal([]byte(jsonStr), &node); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if node.ID != "node-1" || node.Label != "Test Node" {
		t.Fatalf("unexpected node: %+v", node)
	}
	if len(node.Interfaces) != 1 || node.Interfaces[0].ID != "int-1" {
		t.Fatalf("unexpected interfaces: %+v", node.Interfaces)
	}
	if len(node.Services) != 1 || !node.Services[0].Receiver {
		t.Fatalf("unexpected services: %+v", node.Services)
	}
}

func TestResourceUnmarshal(t *testing.T) {
	jsonStr := `{
		"id": "sender-1",
		"label": "Camera 1",
		"version": "1.0.0",
		"type": "sender",
		"node_id": "node-1",
		"device_id": "device-1",
		"manifest": {
			"media_type": "video/raw",
			"essence": "raw",
			"sampling": "YCbCr:4:2:2:10bit"
		}
	}`
	var resource Resource
	if err := json.Unmarshal([]byte(jsonStr), &resource); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resource.ID != "sender-1" || resource.Type != "sender" {
		t.Fatalf("unexpected resource: %+v", resource)
	}
	if resource.Manifest.MediaType != "video/raw" || resource.Manifest.Essence != "raw" {
		t.Fatalf("unexpected manifest: %+v", resource.Manifest)
	}
}

func TestReflectTypes(t *testing.T) {
	// Ensure types are reflectable (needed for Kubernetes API types)
	node := Node{ID: "n1", Label: "test"}
	_ = reflect.TypeOf(node)

	resource := Resource{ID: "r1", Type: "sender"}
	_ = reflect.TypeOf(resource)

	match := SenderMatch{MediaType: "video/raw"}
	_ = reflect.TypeOf(match)

	manifest := Manifest{MediaType: "video/raw"}
	_ = reflect.TypeOf(manifest)
}
