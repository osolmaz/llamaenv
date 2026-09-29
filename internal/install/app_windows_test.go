//go:build windows

package install

import (
	"context"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

const family = "4e440e29-8151-4465-b20f-fb3acb283925_et8rgxdxvrbdj"

func TestTheAppStartsFromExplorerOnTheDesktop(t *testing.T) {
	want := [][]string{{`C:\Windows\explorer.exe`, `shell:AppsFolder\` + family + `!App`}}
	if got := startCommands(family, true); !reflect.DeepEqual(got, want) {
		t.Errorf("got %q", got)
	}
}

// From a remote shell, a one-time interactive task starts the app in the
// user's desktop session, and is deleted after it ran.
func TestTheAppStartsThroughAOneTimeTaskFromARemoteShell(t *testing.T) {
	got := startCommands(family, false)
	if len(got) != 4 {
		t.Fatalf("got %d commands: %q", len(got), got)
	}
	create, run, del := strings.Join(got[0], " "), strings.Join(got[1], " "), strings.Join(got[3], " ")
	app := `C:\Windows\explorer.exe shell:AppsFolder\` + family + `!App`
	if !strings.Contains(create, "/create /tn "+startTask+" /tr "+app) || !strings.HasSuffix(create, "/it /f") {
		t.Errorf("create: %s", create)
	}
	if run != "schtasks.exe /run /tn "+startTask || del != "schtasks.exe /delete /tn "+startTask+" /f" {
		t.Errorf("run: %s, delete: %s", run, del)
	}
	if got[2][0] != "powershell" || got[2][len(got[2])-1] != waitTaskScript {
		t.Errorf("wait: %q", got[2])
	}
}

func TestTheAppScriptsParse(t *testing.T) {
	for name, script := range map[string]string{"stop": stopAppScript, "wait": waitTaskScript} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		check := `$errs = $null; [void][System.Management.Automation.Language.Parser]::ParseInput($env:SCRIPT, [ref]$null, [ref]$errs); $errs | ForEach-Object { $_.Message }`
		cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", check)
		cmd.Env = append(cmd.Environ(), "SCRIPT="+script)
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil || strings.TrimSpace(string(out)) != "" {
			t.Errorf("%s script: %v %s", name, err, out)
		}
	}
}
