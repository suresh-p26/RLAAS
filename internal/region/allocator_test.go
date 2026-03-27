package region

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAllocateGlobalLimit(t *testing.T) {
	alloc := AllocateGlobalLimit(10000, []RegionWeight{{Region: "US", Weight: 5}, {Region: "EU", Weight: 3}, {Region: "APAC", Weight: 2}})
	require.Len(t, alloc, 3, "expected 3 allocations")
	var total int64
	for _, a := range alloc {
		total += a.Limit
	}
	assert.Equal(t, int64(10000), total, "expected exact total allocation")
}

func TestAllocateGlobalLimitEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		limit   int64
		weights []RegionWeight
	}{
		{"zero limit produces empty allocation", 0, []RegionWeight{{Region: "US", Weight: 1}}},
		{"invalid weights produce empty allocation", 100, []RegionWeight{{Region: "", Weight: 1}, {Region: "US", Weight: 0}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AllocateGlobalLimit(tt.limit, tt.weights)
			assert.Empty(t, got)
		})
	}
}

func TestRegionalOverflow(t *testing.T) {
	alloc := []Allocation{{Region: "US", Limit: 5000}, {Region: "EU", Limit: 3000}, {Region: "APAC", Limit: 2000}}
	over := RegionalOverflow(map[string]int64{"US": 5200, "EU": 2900, "APAC": 2200}, alloc)
	assert.Equal(t, int64(200), over["US"], "unexpected US overflow")
	assert.Equal(t, int64(200), over["APAC"], "unexpected APAC overflow")
	_, hasEU := over["EU"]
	assert.False(t, hasEU, "eu should not overflow")
}
