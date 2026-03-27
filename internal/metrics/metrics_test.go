package metrics

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCollector(t *testing.T) {
	c := New()
	require.NotNil(t, c, "expected collector")
	c.DecisionsTotal.Add(1)
	assert.Equal(t, int64(1), c.DecisionsTotal.Load(), "counter should increment")
}
