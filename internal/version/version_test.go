package version

import (
	"strings"
	"testing"
)

func TestInfo(t *testing.T) {
	Version = "v1.2.3"
	Commit = "abc1234"
	BuildTime = "2025-01-01T00:00:00Z"

	got := Info()
	for _, want := range []string{"v1.2.3", "abc1234", "2025-01-01T00:00:00Z"} {
		if !strings.Contains(got, want) {
			t.Errorf("Info() = %q, missing %q", got, want)
		}
	}
}
