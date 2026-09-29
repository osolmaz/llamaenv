//go:build !windows && !darwin

package install

// appCandidates is empty: there is no Llama app on Linux.
func appCandidates() []string { return nil }

// restartApp has nothing to do on Linux: there is no Llama app there, and a
// running "llama serve" keeps its program until it is restarted.
func restartApp() (bool, error) { return false, nil }
