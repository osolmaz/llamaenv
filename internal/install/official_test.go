package install

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestOfficialInstallerUsesLlamaAppsScripts(t *testing.T) {
	found := func(string) (string, error) { return "/usr/bin/curl", nil }
	cmd, err := officialInstaller(context.Background(), "windows", found)
	if err != nil || !strings.Contains(strings.Join(cmd.Args, " "), "https://llama.app/install.ps1") {
		t.Errorf("windows: %v %v", cmd, err)
	}
	cmd, err = officialInstaller(context.Background(), "linux", found)
	if err != nil || !strings.Contains(strings.Join(cmd.Args, " "), "https://llama.app/install.sh") {
		t.Errorf("linux: %v %v", cmd, err)
	}
	missing := func(string) (string, error) { return "", errors.New("not found") }
	if _, err := officialInstaller(context.Background(), "linux", missing); err == nil || !strings.Contains(err.Error(), "curl") {
		t.Errorf("no curl: %v", err)
	}
}
