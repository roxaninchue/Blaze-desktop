package ocr

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Windows 10+ ships Windows.Media.Ocr; Windows PowerShell 5.1 can reach it through WinRT.
const winScript = `$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.Encoding]::UTF8
Add-Type -AssemblyName System.Runtime.WindowsRuntime
$null = [Windows.Storage.StorageFile, Windows.Storage, ContentType = WindowsRuntime]
$null = [Windows.Media.Ocr.OcrEngine, Windows.Foundation, ContentType = WindowsRuntime]
$null = [Windows.Graphics.Imaging.BitmapDecoder, Windows.Graphics, ContentType = WindowsRuntime]
$asTask = [System.WindowsRuntimeSystemExtensions].GetMethods() | Where-Object { $_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation` + "`" + `1' } | Select-Object -First 1
function Await($op, [Type]$t) { $task = $asTask.MakeGenericMethod($t).Invoke($null, @($op)); $null = $task.Wait(-1); $task.Result }
$engine = [Windows.Media.Ocr.OcrEngine]::TryCreateFromUserProfileLanguages()
if ($null -eq $engine) { Write-Error 'NOENGINE' }
if ($args.Count -eq 0) { 'ready'; exit 0 }
$file = Await ([Windows.Storage.StorageFile]::GetFileFromPathAsync($args[0])) ([Windows.Storage.StorageFile])
$stream = Await ($file.OpenAsync([Windows.Storage.FileAccessMode]::Read)) ([Windows.Storage.Streams.IRandomAccessStream])
$decoder = Await ([Windows.Graphics.Imaging.BitmapDecoder]::CreateAsync($stream)) ([Windows.Graphics.Imaging.BitmapDecoder])
$bitmap = Await ($decoder.GetSoftwareBitmapAsync()) ([Windows.Graphics.Imaging.SoftwareBitmap])
$result = Await ($engine.RecognizeAsync($bitmap)) ([Windows.Media.Ocr.OcrResult])
$result.Lines | ForEach-Object { $_.Text }
`

// scriptFile writes the script once per version (-File passes the image path as $args).
func scriptFile() (string, error) {
	sum := sha256.Sum256([]byte(winScript))
	p := filepath.Join(os.TempDir(), fmt.Sprintf("blaze-ocr-%x.ps1", sum[:6]))
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	// UTF-8 with BOM so Windows PowerShell 5.1 reads it as UTF-8.
	if err := os.WriteFile(p, append([]byte{0xEF, 0xBB, 0xBF}, winScript...), 0o600); err != nil {
		return "", err
	}
	return p, nil
}

func run(ctx context.Context, args ...string) (string, error) {
	sf, err := scriptFile()
	if err != nil {
		return "", err
	}
	a := append([]string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", sf}, args...)
	c := exec.CommandContext(ctx, "powershell.exe", a...)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	if err := c.Run(); err != nil {
		if strings.Contains(errb.String(), "NOENGINE") {
			return "", ErrNoEngine
		}
		msg := strings.TrimSpace(errb.String())
		if i := strings.IndexByte(msg, '\n'); i > 0 {
			msg = msg[:i]
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New("Windows OCR: " + msg)
	}
	return out.String(), nil
}

func recognise(ctx context.Context, path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return run(ctx, abs)
}

func engine() (string, string) {
	if _, err := exec.LookPath("powershell.exe"); err != nil {
		return "", "Windows PowerShell isn't available, so Windows OCR can't run"
	}
	return "Windows", "Windows.Media.Ocr, in your Windows display languages"
}
