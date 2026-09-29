//go:build windows

package install

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// startTask is the one-time task that starts the Llama app in the user's
// desktop session. It exists only while it runs.
const startTask = "llamaenv-start-llama-app"

// stopAppScript stops the Llama app and the llama server it started, and
// prints the app's package family name. It exits with 2 when the app is not
// installed, and with 3 when it is not running.
const stopAppScript = `
$ErrorActionPreference = 'Stop'
$p = Get-AppxPackage -Name '` + llamaAppPackage + `'
if (-not $p) { exit 2 }
$procs = Get-Process | Where-Object { $_.Path -and $_.Path.StartsWith($p.InstallLocation) }
if (-not $procs) { exit 3 }
$procs | Stop-Process -Force
# The app is packaged, so Windows keeps its files under Packages\<family>.
$pidFiles = @(
  (Join-Path $env:LOCALAPPDATA ('Packages\' + $p.PackageFamilyName + '\LocalCache\Local\Llama\.llama.pid')),
  (Join-Path $env:LOCALAPPDATA 'Llama\.llama.pid')
)
foreach ($pidFile in $pidFiles) {
  if (Test-Path $pidFile) {
    $serverPid = [int](Get-Content $pidFile -Raw).Trim()
    $server = Get-Process -Id $serverPid -ErrorAction SilentlyContinue
    if ($server -and $server.ProcessName -eq 'llama') { Stop-Process -Id $serverPid -Force }
  }
}
Start-Sleep -Milliseconds 500
Write-Output $p.PackageFamilyName
exit 0
`

// restartApp stops the Llama app and the llama server it started, then
// starts the app again on the user's desktop, so its next server start looks
// up llama.exe anew. It reports false when the app is not installed or not
// running.
func restartApp() (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", stopAppScript)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) && (ee.ExitCode() == 2 || ee.ExitCode() == 3) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, startApp(ctx, strings.TrimSpace(string(out)), onDesktop())
}

// onDesktop says whether llamaenv runs in the session that shows the desktop.
// From a remote shell, such as SSH, it does not, and an app started from it
// would run where nobody sees it.
func onDesktop() bool {
	var own uint32
	if windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &own) != nil {
		return false
	}
	return own == windows.WTSGetActiveConsoleSessionId()
}

func startApp(ctx context.Context, family string, desktop bool) error {
	steps := startCommands(family, desktop)
	for _, args := range steps {
		// G204: fixed Windows programs; the family name comes from Get-AppxPackage.
		if err := exec.CommandContext(ctx, args[0], args[1:]...).Run(); err != nil { //nolint:gosec // see above
			if !desktop {
				_ = exec.CommandContext(ctx, "schtasks.exe", "/delete", "/tn", startTask, "/f").Run()
			}
			return err
		}
	}
	return nil
}

// startCommands returns the commands that start the Llama app. On the
// desktop, Explorer starts it. Elsewhere, a one-time task with an
// interactive logon starts it in the user's desktop session, without a
// password; the task is deleted once it has run.
func startCommands(family string, desktop bool) [][]string {
	app := `C:\Windows\explorer.exe shell:AppsFolder\` + family + `!App`
	if desktop {
		return [][]string{strings.SplitN(app, " ", 2)}
	}
	return [][]string{
		{"schtasks.exe", "/create", "/tn", startTask, "/tr", app, "/sc", "once", "/st", "23:59", "/it", "/f"},
		{"schtasks.exe", "/run", "/tn", startTask},
		{"powershell", "-NoProfile", "-Command", waitTaskScript},
		{"schtasks.exe", "/delete", "/tn", startTask, "/f"},
	}
}

// waitTaskScript waits up to 30 seconds for the start task to have run and
// finished, so that deleting it cannot cancel the start. 0x41303 is
// SCHED_S_TASK_HAS_NOT_RUN.
const waitTaskScript = `
foreach ($i in 1..60) {
  $t = Get-ScheduledTask -TaskName '` + startTask + `' -ErrorAction SilentlyContinue
  if (-not $t) { break }
  $ran = (Get-ScheduledTaskInfo -TaskName '` + startTask + `').LastTaskResult -ne 0x41303
  if ($ran -and $t.State -ne 'Running') { break }
  Start-Sleep -Milliseconds 500
}
`
