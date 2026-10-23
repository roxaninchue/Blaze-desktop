//go:build !windows && !darwin

package ocr

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
)

// Linux has no OCR built in; Tesseract is one package away on every distro.
func recognise(ctx context.Context, path string) (string, error) {
	if _, err := exec.LookPath("tesseract"); err != nil {
		return "", ErrNoEngine
	}
	c := exec.CommandContext(ctx, "tesseract", path, "-", "--psm", "3")
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	if err := c.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New("tesseract: " + msg)
	}
	return out.String(), nil
}

func engine() (string, string) {
	if _, err := exec.LookPath("tesseract"); err != nil {
		return "", "Install tesseract for on-device text recognition (e.g. sudo apt install tesseract-ocr, sudo pacman -S tesseract tesseract-data-eng)"
	}
	return "Tesseract", "Tesseract OCR, on this computer"
}
