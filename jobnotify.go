package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"blaze-desktop/internal/config"
	"blaze-desktop/internal/notify"
)

// jobNotify tells the user a scheduled job ran. Scheduled jobs run without the app,
// so this uses the cached command method (notify.json's fallback), never a probe UI.
func jobNotify(name string, code int) {
	var p Prefs
	b, _ := os.ReadFile(filepath.Join(config.Dir(), "desktop.json"))
	_ = json.Unmarshal(b, &p)
	if p.Notify.Off || p.Notify.NoJobs || notifyDenied(p, "job") {
		return
	}
	c := notify.Load()
	var m *notify.Method
	if c != nil {
		m = c.Fallback
		if m == nil && c.Primary != nil && c.Primary.Kind == "exec" {
			m = c.Primary
		}
	}
	if m == nil {
		execs, _ := notify.ExecCandidates()
		if len(execs) == 0 {
			return
		}
		m = &execs[0]
	}
	if f, t := p.Notify.quiet(); notify.InQuiet(time.Now(), f, t) && code == 0 {
		return // a job that worked can wait until morning; a failure still shows
	}
	n := notify.Note{ID: newID(), Kind: "job", Title: "Scheduled job “" + name + "” finished", Body: lastLines(name)}
	if code != 0 {
		n.Title = "Scheduled job “" + name + "” failed"
		n.Urgency = "critical"
	}
	n.Clean()
	e := notify.Entry{Note: n, At: time.Now(), Method: m.Name}
	if err := notify.RunExec(*m, n); err != nil {
		e.Err = err.Error()
	}
	notify.Record(e)
}

// lastLines is the end of the job's newest log entry.
func lastLines(name string) string {
	b, err := os.ReadFile(filepath.Join(config.Dir(), "logs", name+".log"))
	if err != nil {
		return "Open Blaze › Schedule for the log."
	}
	s := string(b)
	if i := strings.LastIndex(s, "\n=== "); i >= 0 {
		s = s[i:]
		if j := strings.Index(s[1:], "\n"); j >= 0 {
			s = s[j+2:]
		}
	}
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		s = "…" + s[len(s)-200:]
	}
	if s == "" {
		s = "Done."
	}
	return s
}
