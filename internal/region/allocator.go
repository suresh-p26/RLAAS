package region

import (
	"sort"
	"sync"
	"time"
)

// RegionWeight defines one region's share weight.
type RegionWeight struct {
	Region string
	Weight int64
}

// Allocation describes computed regional limit allocation.
type Allocation struct {
	Region string `json:"region"`
	Limit  int64  `json:"limit"`
}

// AllocateGlobalLimit splits a global limit across regions proportionally.
func AllocateGlobalLimit(globalLimit int64, weights []RegionWeight) []Allocation {
	if globalLimit <= 0 || len(weights) == 0 {
		return nil
	}
	valid := make([]RegionWeight, 0, len(weights))
	var totalWeight int64
	for _, w := range weights {
		if w.Region == "" || w.Weight <= 0 {
			continue
		}
		valid = append(valid, w)
		totalWeight += w.Weight
	}
	if totalWeight == 0 {
		return nil
	}
	sort.Slice(valid, func(i, j int) bool { return valid[i].Region < valid[j].Region })
	out := make([]Allocation, 0, len(valid))
	var assigned int64
	for i, w := range valid {
		limit := globalLimit * w.Weight / totalWeight
		if i == len(valid)-1 {
			limit = globalLimit - assigned
		}
		if limit < 0 {
			limit = 0
		}
		out = append(out, Allocation{Region: w.Region, Limit: limit})
		assigned += limit
	}
	return out
}

// RegionalOverflow reports how much each region exceeded its allocated limit.
func RegionalOverflow(usage map[string]int64, allocation []Allocation) map[string]int64 {
	if len(usage) == 0 || len(allocation) == 0 {
		return map[string]int64{}
	}
	limits := make(map[string]int64, len(allocation))
	for _, a := range allocation {
		limits[a.Region] = a.Limit
	}
	out := map[string]int64{}
	for region, used := range usage {
		over := used - limits[region]
		if over > 0 {
			out[region] = over
		}
	}
	return out
}

// ------------------------------------------------
// Health-aware dynamic allocator with failover
// ------------------------------------------------

// RegionHealth represents the health status of a region.
type RegionHealth int

const (
	RegionHealthy   RegionHealth = 0
	RegionDegraded  RegionHealth = 1
	RegionUnhealthy RegionHealth = 2
)

// RegionStatus holds runtime health info for one region.
type RegionStatus struct {
	Region        string
	Health        RegionHealth
	LastHeartbeat time.Time
	CurrentUsage  int64
}

// DynamicAllocator manages region allocations with health awareness,
// automatic failover, and periodic rebalancing.
type DynamicAllocator struct {
	mu           sync.RWMutex
	globalLimit  int64
	weights      []RegionWeight
	allocations  []Allocation
	health       map[string]*RegionStatus
	heartbeatTTL time.Duration
}

// NewDynamicAllocator creates a health-aware allocator.
func NewDynamicAllocator(globalLimit int64, weights []RegionWeight, heartbeatTTL time.Duration) *DynamicAllocator {
	if heartbeatTTL <= 0 {
		heartbeatTTL = 30 * time.Second
	}
	da := &DynamicAllocator{
		globalLimit:  globalLimit,
		weights:      weights,
		health:       make(map[string]*RegionStatus, len(weights)),
		heartbeatTTL: heartbeatTTL,
	}
	for _, w := range weights {
		da.health[w.Region] = &RegionStatus{
			Region:        w.Region,
			Health:        RegionHealthy,
			LastHeartbeat: time.Now(),
		}
	}
	da.rebalance()
	return da
}

// Heartbeat records a health signal from a region.
func (da *DynamicAllocator) Heartbeat(region string, usage int64) {
	da.mu.Lock()
	defer da.mu.Unlock()
	if s, ok := da.health[region]; ok {
		s.LastHeartbeat = time.Now()
		s.CurrentUsage = usage
		if s.Health == RegionUnhealthy {
			s.Health = RegionHealthy
			da.rebalanceLocked()
		}
	}
}

// CheckHealth marks regions without recent heartbeats as unhealthy
// and redistributes their share to healthy regions.
func (da *DynamicAllocator) CheckHealth() {
	da.mu.Lock()
	defer da.mu.Unlock()
	now := time.Now()
	changed := false
	for _, s := range da.health {
		if now.Sub(s.LastHeartbeat) > da.heartbeatTTL && s.Health != RegionUnhealthy {
			s.Health = RegionUnhealthy
			changed = true
		}
	}
	if changed {
		da.rebalanceLocked()
	}
}

// Allocations returns the current allocations (thread-safe copy).
func (da *DynamicAllocator) Allocations() []Allocation {
	da.mu.RLock()
	defer da.mu.RUnlock()
	out := make([]Allocation, len(da.allocations))
	copy(out, da.allocations)
	return out
}

// HealthStatus returns the current health of all regions.
func (da *DynamicAllocator) HealthStatus() []RegionStatus {
	da.mu.RLock()
	defer da.mu.RUnlock()
	out := make([]RegionStatus, 0, len(da.health))
	for _, s := range da.health {
		out = append(out, *s)
	}
	return out
}

func (da *DynamicAllocator) rebalance() {
	da.rebalanceLocked()
}

func (da *DynamicAllocator) rebalanceLocked() {
	// Build weights for healthy regions only; unhealthy weight goes to remaining.
	healthy := make([]RegionWeight, 0, len(da.weights))
	for _, w := range da.weights {
		if s, ok := da.health[w.Region]; ok && s.Health != RegionUnhealthy {
			healthy = append(healthy, w)
		}
	}
	if len(healthy) == 0 {
		// All unhealthy — allocate evenly as a last resort.
		da.allocations = AllocateGlobalLimit(da.globalLimit, da.weights)
		return
	}
	da.allocations = AllocateGlobalLimit(da.globalLimit, healthy)
}

// DrainRegion marks a region as unhealthy and redistributes capacity.
func (da *DynamicAllocator) DrainRegion(region string) {
	da.mu.Lock()
	defer da.mu.Unlock()
	if s, ok := da.health[region]; ok {
		s.Health = RegionUnhealthy
		da.rebalanceLocked()
	}
}

// RestoreRegion marks a region as healthy and rebalances.
func (da *DynamicAllocator) RestoreRegion(region string) {
	da.mu.Lock()
	defer da.mu.Unlock()
	if s, ok := da.health[region]; ok {
		s.Health = RegionHealthy
		s.LastHeartbeat = time.Now()
		da.rebalanceLocked()
	}
}
