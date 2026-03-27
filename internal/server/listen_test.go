package server

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestListenAndServeInvalidAddress(t *testing.T) {
	s := &HTTPServer{Addr: ":-1", Mux: nil}
	err := s.ListenAndServe()
	require.Error(t, err, "expected listen error for invalid address")
}
