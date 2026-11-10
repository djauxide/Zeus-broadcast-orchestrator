package security

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ErrUnauthenticated is returned when no valid credentials are presented.
var ErrUnauthenticated = errors.New("unauthenticated")

// ErrForbidden is returned when the principal lacks permission for an action.
var ErrForbidden = errors.New("forbidden")

// Principal represents an authenticated operator or service account.
type Principal struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Roles    []string `json:"roles"`
	Groups   []string `json:"groups,omitempty"`
	AuthTime time.Time `json:"authTime"`
	Method   string   `json:"method"` // apikey, jwt, mtls
}

// HasRole returns true if the principal holds any of the given roles.
func (p *Principal) HasRole(roles ...string) bool {
	for _, r := range roles {
		for _, pr := range p.Roles {
			if pr == r {
				return true
			}
		}
	}
	return false
}

// Authenticator validates credentials and returns a Principal.
type Authenticator interface {
	Authenticate(ctx context.Context, r *http.Request) (*Principal, error)
}

// APIKeyAuth authenticates via X-API-Key header or Authorization: Apikey <key>.
type APIKeyAuth struct {
	mu      sync.RWMutex
	keys    map[string]*Principal // hashed key -> principal
	logger  *log.Logger
}

// NewAPIKeyAuth creates an API key authenticator.
func NewAPIKeyAuth(logger *log.Logger) *APIKeyAuth {
	return &APIKeyAuth{keys: make(map[string]*Principal), logger: logger}
}

// RegisterKey adds an API key for a principal. The key is stored as a SHA-256 hash.
func (a *APIKeyAuth) RegisterKey(rawKey string, id, name string, roles ...string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	sum := sha256.Sum256([]byte(rawKey))
	a.keys[hex.EncodeToString(sum[:])] = &Principal{
		ID:       id,
		Name:     name,
		Roles:    roles,
		AuthTime: time.Now(),
		Method:   "apikey",
	}
	a.logger.Printf("registered API key for principal %s (%s) roles=%v", id, name, roles)
}

// Authenticate extracts and validates the API key from the request.
func (a *APIKeyAuth) Authenticate(ctx context.Context, r *http.Request) (*Principal, error) {
	key := extractAPIKey(r)
	if key == "" {
		return nil, ErrUnauthenticated
	}
	sum := sha256.Sum256([]byte(key))
	hashed := hex.EncodeToString(sum[:])

	a.mu.RLock()
	p, ok := a.keys[hashed]
	a.mu.RUnlock()
	if !ok {
		return nil, ErrUnauthenticated
	}
	// Return a copy with fresh auth time.
	clone := *p
	clone.AuthTime = time.Now()
	return &clone, nil
}

// extractAPIKey reads X-API-Key or Authorization: Apikey <key>.
func extractAPIKey(r *http.Request) string {
	if v := r.Header.Get("X-API-Key"); v != "" {
		return v
	}
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Apikey ") {
		return strings.TrimSpace(strings.TrimPrefix(auth, "Apikey "))
	}
	return ""
}

// MTLSAuth authenticates via verified mTLS client certificate subject.
type MTLSAuth struct {
	logger *log.Logger
}

// NewMTLSAuth creates an mTLS authenticator. Requires the TLS handler to
// populate r.TLS.PeerCertificates (configured via tls.Config.ClientAuth).
func NewMTLSAuth(logger *log.Logger) *MTLSAuth {
	return &MTLSAuth{logger: logger}
}

// Authenticate extracts the client certificate subject as the principal.
func (a *MTLSAuth) Authenticate(ctx context.Context, r *http.Request) (*Principal, error) {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return nil, ErrUnauthenticated
	}
	cert := r.TLS.PeerCertificates[0]
	subject := cert.Subject.String()
	// Map common name to principal ID.
	cn := ""
	if len(cert.Subject.CommonName) > 0 {
		cn = string(cert.Subject.CommonName[0])
	}
	if cn == "" {
		cn = subject
	}
	a.logger.Printf("mTLS authenticated subject=%s", subject)
	return &Principal{
		ID:       cn,
		Name:     cn,
		Roles:    []string{"operator"},
		AuthTime: time.Now(),
		Method:   "mtls",
	}, nil
}

// CompositeAuth tries multiple authenticators in order.
type CompositeAuth []Authenticator

func (c CompositeAuth) Authenticate(ctx context.Context, r *http.Request) (*Principal, error) {
	var lastErr error
	for _, a := range c {
		p, err := a.Authenticate(ctx, r)
		if err == nil {
			return p, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// Authorizer checks whether a principal may perform an action on a resource.
type Authorizer interface {
	Authorize(p *Principal, action, resource string) error
}

// RBACAuthorizer implements role-based access control.
type RBACAuthorizer struct {
	mu      sync.RWMutex
	policy  map[string]map[string]bool // role -> action -> allow
	logger  *log.Logger
}

// NewRBACAuthorizer creates a default RBAC authorizer with broadcast roles.
func NewRBACAuthorizer(logger *log.Logger) *RBACAuthorizer {
	r := &RBACAuthorizer{
		policy: make(map[string]map[string]bool),
		logger: logger,
	}
	// Viewer: read-only.
	r.grant("viewer", "get:*")
	r.grant("viewer", "list:*")
	// Operator: read + control operations.
	r.grant("operator", "get:*")
	r.grant("operator", "list:*")
	r.grant("operator", "post:*")
	r.grant("operator", "put:*")
	r.grant("operator", "delete:*")
	r.grant("operator", "route:take")
	r.grant("operator", "device:command")
	// Engineer: operator + configuration.
	r.grant("engineer", "get:*")
	r.grant("engineer", "list:*")
	r.grant("engineer", "post:*")
	r.grant("engineer", "put:*")
	r.grant("engineer", "delete:*")
	r.grant("engineer", "route:take")
	r.grant("engineer", "device:command")
	r.grant("engineer", "config:write")
	// Admin: everything.
	r.grant("admin", "*:*")
	return r
}

func (r *RBACAuthorizer) grant(role, action string) {
	if r.policy[role] == nil {
		r.policy[role] = make(map[string]bool)
	}
	r.policy[role][action] = true
}

// Authorize checks the principal against the action/resource.
func (r *RBACAuthorizer) Authorize(p *Principal, action, resource string) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, role := range p.Roles {
		actions, ok := r.policy[role]
		if !ok {
			continue
		}
		// Wildcard action matches any action.
		if actions["*:*"] || actions[action+":*"] || actions[action+":"+resource] {
			return nil
		}
	}
	r.logger.Printf("RBAC denied principal=%s roles=%v action=%s resource=%s", p.ID, p.Roles, action, resource)
	return ErrForbidden
}

// SecurityContext bundles authenticator and authorizer.
type SecurityContext struct {
	Auth      Authenticator
	Authz     Authorizer
	Audit     AuditLogger
	logger    *log.Logger
}

// NewSecurityContext creates a default security context.
func NewSecurityContext(logger *log.Logger) *SecurityContext {
	auth := CompositeAuth{
		NewAPIKeyAuth(logger),
		NewMTLSAuth(logger),
	}
	return &SecurityContext{
		Auth:   auth,
		Authz:  NewRBACAuthorizer(logger),
		Audit:  NewMemoryAudit(logger),
		logger: logger,
	}
}

// Middleware returns an HTTP middleware that authenticates and authorizes.
// action and resource are passed via context keys ctxKeyAction / ctxKeyResource
// or extracted from the request path/method.
func (sc *SecurityContext) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := sc.Auth.Authenticate(r.Context(), r)
		if err != nil {
			sc.Audit.Log(r, "auth", "", err)
			http.Error(w, `{"error":"unauthenticated"}`, http.StatusUnauthorized)
			return
		}

		action := strings.ToLower(r.Method)
		resource := r.URL.Path
		if err := sc.Authz.Authorize(p, action, resource); err != nil {
			sc.Audit.Log(r, "authz", p.ID, err)
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}

		// Attach principal to context.
		ctx := context.WithValue(r.Context(), principalKey{}, p)
		sc.Audit.Log(r, "allowed", p.ID, nil)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// PrincipalFromContext returns the authenticated principal from context.
func PrincipalFromContext(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(*Principal)
	return p, ok
}

type principalKey struct{}

// ===== Audit Logging =====

// AuditLogger records security-relevant events.
type AuditLogger interface {
	Log(r *http.Request, action, principalID string, err error)
	Entries() []AuditEntry
}

// MemoryAudit stores audit events in memory (for production, use a persistent store).
type MemoryAudit struct {
	mu   sync.Mutex
	entries []AuditEntry
	logger *log.Logger
}

// AuditEntry is a single audit record.
type AuditEntry struct {
	Timestamp  string `json:"timestamp"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	Action     string `json:"action"`
	Principal  string `json:"principal"`
	RemoteAddr string `json:"remoteAddr"`
	Error      string `json:"error,omitempty"`
}

// NewMemoryAudit creates an in-memory audit logger.
func NewMemoryAudit(logger *log.Logger) *MemoryAudit {
	return &MemoryAudit{logger: logger}
}

func (a *MemoryAudit) Log(r *http.Request, action, principalID string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	entry := AuditEntry{
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		Method:     r.Method,
		Path:       r.URL.Path,
		Action:     action,
		Principal:  principalID,
		RemoteAddr: r.RemoteAddr,
	}
	if err != nil {
		entry.Error = err.Error()
	}
	a.entries = append(a.entries, entry)
	// Keep only the last 10000 entries in memory.
	if len(a.entries) > 10000 {
		a.entries = a.entries[1:]
	}
	if err != nil {
		a.logger.Printf("AUDIT DENIED method=%s path=%s principal=%s err=%s", r.Method, r.URL.Path, principalID, err)
	} else {
		a.logger.Printf("AUDIT method=%s path=%s principal=%s action=%s", r.Method, r.URL.Path, principalID, action)
	}
}

// Entries returns a copy of all audit entries.
func (a *MemoryAudit) Entries() []AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]AuditEntry, len(a.entries))
	copy(out, a.entries)
	return out
}

var _ Authenticator = (*APIKeyAuth)(nil)
var _ Authenticator = (*MTLSAuth)(nil)
var _ Authorizer = (*RBACAuthorizer)(nil)
var _ AuditLogger = (*MemoryAudit)(nil)
