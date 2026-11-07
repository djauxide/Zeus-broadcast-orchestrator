package nmos

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"
)

type Client struct {
	BaseURL      string
	TokenURL     string
	ClientID     string
	ClientSecret string
	HTTP         *http.Client

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

func (c *Client) tokenFor(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.expiresAt.Add(-30*time.Second)) {
		return c.token, nil
	}
	body, _ := json.Marshal(map[string]string{
		"grant_type":    "client_credentials",
		"client_id":     c.ClientID,
		"client_secret": c.ClientSecret,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token endpoint %d: %s", resp.StatusCode, b)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", err
	}
	c.token = tok.AccessToken
	c.expiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	return c.token, nil
}

func (c *Client) PatchIS05(ctx context.Context, receiverID string, body []byte) error {
	tok, err := c.tokenFor(ctx)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/x-nmos/connection/v1.1/single/receivers/%s/staged", c.BaseURL, receiverID)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("IS-05 PATCH %d: %s", resp.StatusCode, b)
	}
	return nil
}

// Node represents an IS-04 node discovered from the NMOS registry.
type Node struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Version   string `json:"version"`
	Clock     string `json:"clock,omitempty"`
	Hostname  string `json:"hostname,omitempty"`
	Interfaces []Interface `json:"interfaces,omitempty"`
	Services   []Service   `json:"services,omitempty"`
}

// Interface represents an IS-04 node interface.
type Interface struct {
	ID        string   `json:"id"`
	NodeID    string   `json:"node_id"`
	Link      string   `json:"link,omitempty"`
	MAC       string   `json:"mac,omitempty"`
	IP        []IPAddr `json:"ip,omitempty"`
}

// IPAddr represents an IP address on an IS-04 interface.
type IPAddr struct {
	Address    string `json:"address,omitempty"`
	Family     string `json:"family,omitempty"`
	Gateway    string `json:"gateway,omitempty"`
	Netmask    string `json:"netmask,omitempty"`
	CIDR       string `json:"cidr,omitempty"`
	Type       string `json:"type,omitempty"`
	Vlan       int    `json:"vlan,omitempty"`
}

// Service represents an IS-04 node service.
type Service struct {
	ID        string `json:"id"`
	DeviceID  string `json:"device_id,omitempty"`
	Receiver  bool   `json:"receiver,omitempty"`
	Sender    bool   `json:"sender,omitempty"`
	API       API    `json:"api,omitempty"`
	Resource  string `json:"resource,omitempty"`
}

// API represents an IS-04 service API endpoint.
type API struct {
	Versions []Version `json:"versions,omitempty"`
	URL      string    `json:"url,omitempty"`
}

// Version represents an API version entry.
type Version struct {
	Version string `json:"version,omitempty"`
}

// Resource represents an IS-04 resource (sender, receiver, device, etc.).
type Resource struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Version     string `json:"version"`
	Parent      string `json:"parent,omitempty"`
	NodeID      string `json:"node_id,omitempty"`
	DeviceID    string `json:"device_id,omitempty"`
	Type        string `json:"type,omitempty"`
	Manifest    Manifest `json:"manifest,omitempty"`
}

// Manifest represents an IS-04 sender manifest.
type Manifest struct {
	MediaType   string `json:"media_type,omitempty"`
	Essence     string `json:"essence,omitempty"`
	Sampling    string `json:"sampling,omitempty"`
	ChannelCount int   `json:"channel_count,omitempty"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	Interlace   string `json:"interlace,omitempty"`
}

// SenderMatch describes the criteria for matching an IS-04 sender.
type SenderMatch struct {
	MediaType  string
	Essence    string
	Sampling   string
}

// ListNodes queries GET /x-nmos/query/v1.4/nodes from the NMOS registry.
func (c *Client) ListNodes(ctx context.Context) ([]Node, error) {
	tok, err := c.tokenFor(ctx)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/x-nmos/query/v1.4/nodes", c.BaseURL)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("IS-04 list nodes: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("IS-04 list nodes %d: %s", resp.StatusCode, b)
	}
	var nodes []Node
	if err := json.NewDecoder(resp.Body).Decode(&nodes); err != nil {
		return nil, fmt.Errorf("IS-04 decode nodes: %w", err)
	}
	return nodes, nil
}

// ListResources queries GET /x-nmos/query/v1.4/resources from the NMOS registry.
func (c *Client) ListResources(ctx context.Context) ([]Resource, error) {
	tok, err := c.tokenFor(ctx)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/x-nmos/query/v1.4/resources", c.BaseURL)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("IS-04 list resources: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("IS-04 list resources %d: %s", resp.StatusCode, b)
	}
	var resources []Resource
	if err := json.NewDecoder(resp.Body).Decode(&resources); err != nil {
		return nil, fmt.Errorf("IS-04 decode resources: %w", err)
	}
	return resources, nil
}

// FindSender returns the first IS-04 sender resource whose manifest matches
// the given SenderMatch criteria. Empty criteria match any value.
func (c *Client) FindSender(ctx context.Context, match SenderMatch) (*Resource, error) {
	resources, err := c.ListResources(ctx)
	if err != nil {
		return nil, err
	}
	// Sort by label for deterministic selection.
	sort.Slice(resources, func(i, j int) bool {
		return resources[i].Label < resources[j].Label
	})
	for i := range resources {
		r := &resources[i]
		if r.Type != "sender" {
			continue
		}
		m := r.Manifest
		if match.MediaType != "" && m.MediaType != match.MediaType {
			continue
		}
		if match.Essence != "" && m.Essence != match.Essence {
			continue
		}
		if match.Sampling != "" && m.Sampling != match.Sampling {
			continue
		}
		return r, nil
	}
	return nil, fmt.Errorf("no IS-04 sender found matching media_type=%q essence=%q sampling=%q",
		match.MediaType, match.Essence, match.Sampling)
}
