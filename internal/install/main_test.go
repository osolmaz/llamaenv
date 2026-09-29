package install

import (
	"os"
	"testing"
)

// TestMain keeps every test away from the real Llama app and its files.
func TestMain(m *testing.M) {
	AppCandidates = func() []string { return nil }
	RestartApp = func() (bool, error) { return false, nil }
	os.Exit(m.Run())
}
