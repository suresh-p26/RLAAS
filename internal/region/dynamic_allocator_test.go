package region

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDynamicAllocator_Basic(t *testing.T) {
	weights := []RegionWeight{
		{Region: "US", Weight: 5},
		{Region: "EU", Weight: 3},
		{Region: "APAC", Weight: 2},
	}
	da := NewDynamicAllocator(10000, weights, 30*time.Second)

	allocs := da.Allocations()
	require.Len(t, allocs, 3, "expected 3 allocations")
	var total int64
	for _, a := range allocs {
		total += a.Limit
	}
	assert.Equal(t, int64(10000), total, "total should equal 10000")
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
	_, apacPresent := allocMap["APAC"]
	assert.False(t, apacPresent, "APAC should not be in allocations after drain")
	assert.Equal(t, int64(10000), total, "total should still be 10000")

	// Health should show APAC unhealthy.
	for _, s := range da.HealthStatus() {
		if s.Region == "APAC" {
			assert.Equal(t, RegionUnhealthy, s.Health, "APAC should be unhealthy")
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
	require.Len(t, allocs, 3, "expected 3 allocations after restore")
}

func TestDynamicAllocator_Heartbeat(t *testing.T) {
	weights := []RegionWeight{
		{Region: "US", Weight: 5},
		{Region: "EU", Weight: 3},
	}
	da := NewDynamicAllocator(1000, weights, 30*time.Second)

	da.Heartbeat("US", 250)

	for _, s := range da.HealthStatus() {
		if s.Region == "US" {
			assert.Equal(t, int64(250), s.CurrentUsage, "US usage should be 250")
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
		assert.NotEqual(t, "EU", a.Region, "EU should not be allocated after drain")
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
	assert.True(t, found, "EU should be in allocations after heartbeat")
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
		assert.Equal(t, RegionUnhealthy, s.Health, "region %s should be unhealthy after TTL expiry", s.Region)
	}

	// All unhealthy should still produce allocations (last-resort).
	allocs := da.Allocations()
	assert.NotEmpty(t, allocs, "should produce fallback allocations when all unhealthy")
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
	assert.Equal(t, int64(100), total, "all-unhealthy fallback total should be 100")
}

func TestDynamicAllocator_DefaultHeartbeatTTL(t *testing.T) {
	da := NewDynamicAllocator(100, []RegionWeight{{Region: "A", Weight: 1}}, 0)
	assert.Equal(t, 30*time.Second, da.heartbeatTTL, "default heartbeat TTL should be 30s")
}
