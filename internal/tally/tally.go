package tally

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// TallyState is the state of a signal at a destination.
type TallyState string

const (
	TallyNone     TallyState = "none"
	TallySafe     TallyState = "safe"      // Signal routed to destination but not on air
	TallyPreview  TallyState = "preview"   // Signal previewed at destination
	TallyProgram  TallyState = "program"   // Signal live on air at destination
	TallyProtect  TallyState = "protect"   // Destination protected from routing changes
	TallyFault    TallyState = "fault"     // Signal path faulted
)

// Tally represents a tally observation for a destination.
type Tally struct {
	DestinationID string    `json:"destinationID"`
	SourceID      string    `json:"sourceID,omitempty"`
	State         TallyState `json:"state"`
	RouterID      string    `json:"routerID,omitempty"`
	UpdatedAt     string    `json:"updatedAt"`
	Source        string    `json:"source,omitempty"` // "router", "manual", "nap"
}

// Tracker maintains tally state for all destinations across all routers.
type Tracker struct {
	mu      sync.RWMutex
	tallies map[string]*Tally // key = routerID:destinationID
	logger  *log.Logger
}

// NewTracker creates a tally tracker.
func NewTracker(logger *log.Logger) *Tracker {
	return &Tracker{
		tallies: make(map[string]*Tally),
		logger:  logger,
	}
}

// key builds the map key for a router+destination.
func key(routerID, destinationID string) string {
	return routerID + ":" + destinationID
}

// Set updates the tally for a destination.
func (t *Tracker) Set(routerID, destinationID, sourceID string, state TallyState, source string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	k := key(routerID, destinationID)
	t.tallies[k] = &Tally{
		DestinationID: destinationID,
		SourceID:      sourceID,
		State:         state,
		RouterID:      routerID,
		UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
		Source:        source,
	}
	t.logger.Printf("TALLY router=%s dest=%s src=%s state=%s source=%s",
		routerID, destinationID, sourceID, state, source)
}

// Get returns the current tally for a destination.
func (t *Tracker) Get(routerID, destinationID string) (*Tally, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	tally, ok := t.tallies[key(routerID, destinationID)]
	if !ok {
		return nil, false
	}
	cp := *tally
	return &cp, true
}

// GetAll returns all tallies (optionally filtered by router).
func (t *Tracker) GetAll(routerID string) []*Tally {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var out []*Tally
	for _, tally := range t.tallies {
		if routerID == "" || tally.RouterID == routerID {
			cp := *tally
			out = append(out, &cp)
		}
	}
	return out
}

// RouteApplied is called when a crosspoint route is applied on a router.
// It sets the destination tally to Program for the routed source.
func (t *Tracker) RouteApplied(routerID, sourceID, destinationID string) {
	t.Set(routerID, destinationID, sourceID, TallyProgram, "router")
}

// RouteCleared is called when a crosspoint is cleared.
// It sets the destination tally to None.
func (t *Tracker) RouteCleared(routerID, destinationID string) {
	t.Set(routerID, destinationID, "", TallyNone, "router")
}

// Protect sets a destination to protected state.
func (t *Tracker) Protect(routerID, destinationID string) {
	t.Set(routerID, destinationID, "", TallyProtect, "manual")
}

// Unprotect clears the protect state.
func (t *Tracker) Unprotect(routerID, destinationID string) {
	t.Set(routerID, destinationID, "", TallyNone, "manual")
}

// Preview sets a destination to preview state.
func (t *Tracker) Preview(routerID, sourceID, destinationID string) {
	t.Set(routerID, destinationID, sourceID, TallyPreview, "router")
}

// Fault marks a destination as faulted.
func (t *Tracker) Fault(routerID, destinationID string) {
	t.Set(routerID, destinationID, "", TallyFault, "router")
}

// String returns a human-readable representation of the tally state.
func (s TallyState) String() string {
	return string(s)
}

var _ = fmt.Sprintf // keep fmt imported for potential future use
