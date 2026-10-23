package longchat

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"blaze-desktop/internal/config"
)

// AsideOver is the size past which a tool result goes to an artifact file.
const AsideOver = 4000

const headLines, tailLines = 40, 20

func artifactDir() string { return filepath.Join(config.Dir(), "artifacts") }

var idRe = regexp.MustCompile(`^[a-f0-9]{12}$`)

// Aside saves a big tool result to ~/.blaze/artifacts/<id>.txt and returns what the
// model sees instead: the first 40 lines, the last 20 and "artifact:<id> (N lines)".
// Small results come back unchanged.
func Aside(res string) string {
	if len(res) <= AsideOver {
		return res
	}
	sum := sha1.Sum([]byte(res))
	id := hex.EncodeToString(sum[:])[:12]
	if err := os.MkdirAll(artifactDir(), 0o700); err != nil {
		return res
	}
	p := filepath.Join(artifactDir(), id+".txt")
	if _, err := os.Stat(p); err != nil {
		if os.WriteFile(p, []byte(res), 0o600) != nil {
			return res
		}
	}
	lines := strings.Split(res, "\n")
	n := len(lines)
	var b strings.Builder
	if n <= headLines+tailLines {
		// Few but very long lines: show characters instead.
		b.WriteString(res[:2400])
		b.WriteString(fmt.Sprintf("\n… [%d more characters] …\n", len(res)-3200))
		b.WriteString(res[len(res)-800:])
	} else {
		b.WriteString(strings.Join(lines[:headLines], "\n"))
		b.WriteString(fmt.Sprintf("\n… [%d lines not shown] …\n", n-headLines-tailLines))
		b.WriteString(strings.Join(lines[n-tailLines:], "\n"))
	}
	b.WriteString(fmt.Sprintf("\n\nartifact:%s (%d lines, %d characters). The full output is saved; read_artifact {id, from, lines} reads any part of it.", id, n, len(res)))
	return b.String()
}

// ReadArtifact returns lines [from, from+count) of an artifact (1-based), numbered.
func ReadArtifact(id string, from, count int) (string, error) {
	id = strings.TrimPrefix(strings.TrimSpace(id), "artifact:")
	if !idRe.MatchString(id) {
		return "", fmt.Errorf("unknown artifact %q", id)
	}
	b, err := os.ReadFile(filepath.Join(artifactDir(), id+".txt"))
	if err != nil {
		return "", fmt.Errorf("artifact %s is gone", id)
	}
	lines := strings.Split(string(b), "\n")
	if from < 1 {
		from = 1
	}
	if count <= 0 || count > 400 {
		count = 200
	}
	if from > len(lines) {
		return "", fmt.Errorf("artifact %s has %d lines", id, len(lines))
	}
	end := from - 1 + count
	if end > len(lines) {
		end = len(lines)
	}
	var out strings.Builder
	for i := from - 1; i < end; i++ {
		fmt.Fprintf(&out, "%d\t%s\n", i+1, lines[i])
	}
	if end < len(lines) {
		fmt.Fprintf(&out, "… %d more lines (from=%d)\n", len(lines)-end, end+1)
	}
	return out.String(), nil
}
