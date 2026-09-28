//go:build windows

package install

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// The Llama app's package identity (ggml-org/Llama-Windows, Package.appxmanifest).
const llamaAppPackage = "4e440e29-8151-4465-b20f-fb3acb283925"

// addToPath puts dir first in the user PATH (HKCU\Environment\Path) and
// tells running programs that the environment changed. The machine PATH and
// all other user entries stay as they are.
func addToPath(dir string) error {
	return editUserPath(func(entries []string) []string {
		return append([]string{dir}, without(entries, dir)...)
	})
}

func removeFromPath(dir string) error {
	return editUserPath(func(entries []string) []string { return without(entries, dir) })
}

func editUserPath(edit func([]string) []string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer func() { _ = k.Close() }()
	old, typ, err := k.GetStringValue("Path")
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	if typ == 0 {
		typ = registry.EXPAND_SZ
	}
	var entries []string
	for _, e := range strings.Split(old, ";") {
		if strings.TrimSpace(e) != "" {
			entries = append(entries, e)
		}
	}
	value := strings.Join(edit(entries), ";")
	if typ == registry.SZ {
		err = k.SetStringValue("Path", value)
	} else {
		err = k.SetExpandStringValue("Path", value)
	}
	if err != nil {
		return err
	}
	broadcastEnvironmentChange()
	return nil
}

func without(entries []string, dir string) []string {
	var out []string
	for _, e := range entries {
		if !strings.EqualFold(filepath.Clean(strings.Trim(e, `"`)), filepath.Clean(dir)) {
			out = append(out, e)
		}
	}
	return out
}

// broadcastEnvironmentChange sends WM_SETTINGCHANGE("Environment"), so that
// Explorer passes the new PATH to programs started from now on.
func broadcastEnvironmentChange() {
	user32 := windows.NewLazySystemDLL("user32.dll")
	send := user32.NewProc("SendMessageTimeoutW")
	env, _ := syscall.UTF16PtrFromString("Environment")
	const hwndBroadcast, wmSettingChange, smtoAbortIfHung = 0xffff, 0x001A, 0x0002
	var result uintptr
	// G103: the Windows API takes these pointers; they point to Go values
	// that stay alive for the call.
	_, _, _ = send.Call(hwndBroadcast, wmSettingChange, 0,
		uintptr(unsafe.Pointer(env)), smtoAbortIfHung, 5000, uintptr(unsafe.Pointer(&result))) //nolint:gosec // Windows API call, see above
}

// restartApp stops the Llama app and the llama server it started, then
// starts the app again, so its next server start looks up llama.exe anew.
// It reports false when the app is not installed or not running.
func restartApp() (bool, error) {
	script := `
$ErrorActionPreference = 'Stop'
$p = Get-AppxPackage -Name '` + llamaAppPackage + `'
if (-not $p) { exit 2 }
$procs = Get-Process | Where-Object { $_.Path -and $_.Path.StartsWith($p.InstallLocation) }
if (-not $procs) { exit 3 }
$procs | Stop-Process -Force
$pidFile = Join-Path $env:LOCALAPPDATA 'Llama\.llama.pid'
if (Test-Path $pidFile) {
  $serverPid = [int](Get-Content $pidFile -Raw).Trim()
  $server = Get-Process -Id $serverPid -ErrorAction SilentlyContinue
  if ($server -and $server.ProcessName -eq 'llama') { Stop-Process -Id $serverPid -Force }
}
Start-Sleep -Milliseconds 500
Start-Process ('shell:AppsFolder\' + $p.PackageFamilyName + '!App')
exit 0
`
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) && (ee.ExitCode() == 2 || ee.ExitCode() == 3) {
		return false, nil
	}
	return err == nil, err
}

// replaceFile replaces dst, also when dst is a running program: Windows
// allows renaming a running .exe, but not overwriting it.
func replaceFile(tmp, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		old := dst + ".old"
		_ = os.Remove(old) // a leftover from the previous update, if any
		if err := os.Rename(dst, old); err != nil {
			return err
		}
	}
	return os.Rename(tmp, dst)
}

// removeDataDir deletes the data folder. llamaenv.exe in it may be the
// running program, which Windows cannot delete, so a short-lived cmd.exe
// removes the folder after this process exits.
func removeDataDir(dir string) error {
	self, _ := os.Executable()
	if self == "" || !strings.HasPrefix(strings.ToLower(filepath.Clean(self)), strings.ToLower(filepath.Clean(dir))+`\`) {
		return os.RemoveAll(dir)
	}
	// G204: dir is llamaenv's own data folder. The command must outlive this
	// process, so it gets no context that ends with it.
	cmd := exec.CommandContext(context.Background(), "cmd.exe", "/c", "ping -n 3 127.0.0.1 >nul & rmdir /s /q \""+dir+"\"") //nolint:gosec // own data folder, see above
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW | windows.DETACHED_PROCESS}
	return cmd.Start()
}
