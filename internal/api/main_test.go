// Package api_test exercises the Engine end-to-end through the ConnectRPC
// seam: every test drives internal/api's handler over the wire, the same
// way a real client would, and never inspects SQL, tables, or River job
// state directly.
package api_test

import (
	"testing"

	"github.com/use-fabrica/loom/internal/testpg"
)

// TestMain lets testpg own the shared Postgres cluster used by every test
// in this package: it runs the suite, then tears down whatever cluster it
// started for it.
func TestMain(m *testing.M) {
	testpg.Main(m)
}
