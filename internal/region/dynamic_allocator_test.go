package region

import (
	"testing"
	"time"
)

func TestDynamicAllocator_Basic(t *testing.T) {
	weights := []RegionWeight{
		{Region: "US", Weight: 5},
		{Region: "EU", Weight: 3},
		{Region: "APAC", Weight: 2},
	}
	da := NewDynamicAllocator(10000, weights, 30*time.Second)

	allocs := da.Allocations()
	if len(allocs) != 3 {
		t.Fatalf("expected 3 allocations, got %d", len(allocs))
	}
	var total int64
	for _, a := range allocs {
		total += a.Limit
	}
	if total != 10000 {
		t.Fatalf("total should equal 10000, got %d", total)
	}
}

func TestDynamicAllocator_DrainRegion(t *testing.T) {
	weights := []RegionWeight{
		{Region: "US", Weight: 5},
		{Region: "EU", Weight: 3},
		{Region: "APAC", Weight: 2},
	}
	da := NewDynamicAllocator(10000, weights, 30*time.Second)

	da.DrainRegion("APAC")

	allocs := da.Allocations()
	// APAC should not appear in allocations.
	allocMap := map[string]int64{}
	var total int64
	for _, a := range allocs {
		allocMap[a.Region] = a.Limit
		total += a.Limit
	}
	if _, ok := allocMap["APAC"]; ok {
		t.Errorf("APAC should not be in allocations after drain")
	}
	if total != 10000 {
		t.Fatalf("total should still be 10000, got %d", total)
	}

	// Health should show APAC unhealthy.
	for _, s := range da.HealthStatus() {
		if s.Region == "APAC" && s.Health != RegionUnhealthy {
			t.Errorf("APAC should be unhealthy")
		}
	}
}

func TestDynamicAllocator_RestoreRegion(t *testing.T) {
	weights := []RegionWeight{
		{Region: "US", Weight: 5},
		{Region: "EU", Weight: 3},
		{Region: "APAC", Weight: 2},
	}
	da := NewDynamicAllocator(10000, weights, 30*time.Second)

	da.DrainRegion("EU")
	da.RestoreRegion("EU")

	allocs := da.Allocations()
	if len(allocs) != 3 {
		t.Fatalf("expected 3 allocations after restore, got %d", len(allocs))
	}
}

func TestDynamicAllocator_Heartbeat(t *testing.T) {
	weights := []RegionWeight{
		{Region: "US", Weight: 5},
		{Region: "EU", Weight: 3},
	}
	da := NewDynamicAllocator(1000, weights, 30*time.Second)

	da.Heartbeat("US", 250)

	for _, s := range da.HealthStatus() {
		if s.Region == "US" && s.CurrentUsage != 250 {
			t.Errorf("US usage should be 250, got %d", s.CurrentUsage)
		}
	}
}

func TestDynamicAllocator_HeartbeatRestoresUnhealthy(t *testing.T) {
	weights := []RegionWeight{
		{Region: "US", Weight: 5},
		{Region: "EU", Weight: 3},
	}
	da := NewDynamicAllocator(1000, weights, 30*time.Second)

	da.DrainRegion("EU")
	// Confirm EU is out.
	allocs := da.Allocations()
	for _, a := range allocs {
		if a.Region == "EU" {
			t.Fatal("EU should not be allocated after drain")
		}
	}

	// Heartbeat from EU restores it.
	da.Heartbeat("EU", 0)
	allocs = da.Allocations()
	found := false
	for _, a := range allocs {
		if a.Region == "EU" {
			found = true
		}
	}
	if !found {
		t.Fatal("EU should be in allocations after heartbeat")
	}
}

func TestDynamicAllocator_CheckHealth(t *testing.T) {
	weights := []RegionWeight{
		{Region: "US", Weight: 5},
		{Region: "EU", Weight: 3},
	}
	// Use a very short TTL for testing.
	da := NewDynamicAllocator(1000, weights, 10*time.Millisecond)

	// Wait for heartbeat to expire.
	time.Sleep(20 * time.Millisecond)

	da.CheckHealth()

	for _, s := range da.HealthStatus() {
		if s.Health != RegionUnhealthy {
			t.Errorf("region %s should be unhealthy after TTL expiry", s.Region)
		}
	}

	// All unhealthy should still produce allocations (last-resort).
	allocs := da.Allocations()
	if len(allocs) == 0 {
		t.Fatal("should produce fallback allocations when all unhealthy")
	}
}

func TestDynamicAllocator_AllUnhealthyFallback(t *testing.T) {
	weights := []RegionWeight{
		{Region: "A", Weight: 1},
		{Region: "B", Weight: 1},
	}
	da := NewDynamicAllocator(100, weights, 30*time.Second)

	da.DrainRegion("A")
	da.DrainRegion("B")

	allocs := da.Allocations()
	var total int64
	for _, a := range allocs {
		total += a.Limit
	}
	if total != 100 {
		t.Fatalf("all-unhealthy fallback total should be 100, got %d", total)
	}
}

func TestDynamicAllocator_DefaultHeartbeatTTL(t *testing.T) {
	da := NewDynamicAllocator(100, []RegionWeight{{Region: "A", Weight: 1}}, 0)
	if da.heartbeatTTL != 30*time.Second {
		t.Fatalf("default heartbeat TTL should be 30s, got %v", da.heartbeatTTL)
	}
}
