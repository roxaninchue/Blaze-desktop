package main

// CSV report (W2, local half): drop or open a CSV → cleaned table, a group summary, a chart
// and Markdown/PDF exports, all computed on this computer (internal/csvkit). The written
// summary is the model's: after csv_report the agent writes a short summary from the numbers it
// was given and hands it to csv_summary, which puts it into the report (csvkit.Report.SetSummary)
// and rewrites the Markdown and PDF. Tools run on this computer, so this works the same when
// Blaze Cloud answers the turn. Until then the report says the summary is pending. Reachable two ways: the csv_report tool the agent calls, and the CSV
// button on a composer chip / the + menu (CSVReport).

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"

	"blaze-desktop/internal/config"
	"blaze-desktop/internal/csvkit"
	"blaze-desktop/internal/tools"
)

// CSVRequest is what the CSV view (or a chip's CSV button) asks for.
type CSVRequest struct {
	Path    string `json:"path"`
	GroupBy string `json:"group_by"`
	Value   string `json:"value"`
	Agg     string `json:"agg"`
	Chart   string `json:"chart"`
}

// CSVResult is a report for the view, or why there isn't one.
type CSVResult struct {
	Report   *csvkit.Report `json:"report,omitempty"`
	ChartURL string         `json:"chart_url,omitempty"` // /csvkit/chart/<id>.png on the asset server
	Error    string         `json:"error,omitempty"`
}

// csvCharts maps the ids handed out in chart URLs to the PNG/SVG files Blaze wrote, so the
// asset server only ever serves those files.
var csvCharts = struct {
	sync.Mutex
	m       map[string]string
	files   map[string]bool           // every file a report wrote (what CSVOpen may open)
	reports map[string]*csvkit.Report // report id → report (what csv_summary may fill)
}{m: map[string]string{}, files: map[string]bool{}, reports: map[string]*csvkit.Report{}}

func csvRemember(r *csvkit.Report) string {
	_, url := csvRememberID(r)
	return url
}

// csvRememberID registers a report's files and returns its id and chart URL.
func csvRememberID(r *csvkit.Report) (string, string) {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	csvCharts.Lock()
	defer csvCharts.Unlock()
	csvCharts.reports[id] = r
	if r.Files.PNG != "" {
		csvCharts.m[id+".png"] = r.Files.PNG
	}
	csvCharts.m[id+".svg"] = r.Files.SVG
	for _, f := range []string{r.Files.Cleaned, r.Files.SVG, r.Files.PNG, r.Files.MD, r.Files.PDF} {
		if f != "" {
			csvCharts.files[f] = true
		}
	}
	if r.Files.PNG != "" {
		return id, "/csvkit/chart/" + id + ".png"
	}
	return id, "/csvkit/chart/" + id + ".svg"
}

// csvSetSummary puts the model's written summary into a report it made and rewrites its
// Markdown and PDF.
func csvSetSummary(id, text string) (*csvkit.Report, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("the summary is empty")
	}
	if r := []rune(text); len(r) > 4000 {
		text = strings.TrimSpace(string(r[:4000])) + "…"
	}
	csvCharts.Lock()
	r := csvCharts.reports[strings.TrimSpace(id)]
	csvCharts.Unlock()
	if r == nil {
		return nil, fmt.Errorf("no CSV report with id %q in this session; run csv_report first", id)
	}
	csvCharts.Lock() // one writer per report at a time
	defer csvCharts.Unlock()
	if err := r.SetSummary(text); err != nil {
		return nil, err
	}
	return r, nil
}

// csvChartHandler serves /csvkit/chart/<id>.png|svg and hands everything else to next.
func csvChartHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const pre = "/csvkit/chart/"
		if !strings.HasPrefix(r.URL.Path, pre) {
			next.ServeHTTP(w, r)
			return
		}
		csvCharts.Lock()
		p := csvCharts.m[strings.TrimPrefix(r.URL.Path, pre)]
		csvCharts.Unlock()
		if p == "" {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(p, ".svg") {
			w.Header().Set("Content-Type", "image/svg+xml")
		}
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, p)
	})
}

func csvOptions(q CSVRequest) csvkit.Options {
	return csvkit.Options{GroupBy: q.GroupBy, Value: q.Value, Agg: q.Agg, Chart: q.Chart, FallbackDir: filepath.Join(config.Dir(), "desktop", "csv")}
}

// CSVReport cleans the CSV, summarises it and writes the cleaned copy, chart and reports next
// to it (the direct UI action; nothing leaves this computer).
func (a *App) CSVReport(q CSVRequest) CSVResult {
	p := strings.TrimSpace(q.Path)
	if p == "" {
		return CSVResult{Error: "Choose a CSV file first."}
	}
	r, err := csvkit.Run(p, csvOptions(q))
	if err != nil {
		return CSVResult{Error: "Couldn't read that CSV: " + err.Error()}
	}
	return CSVResult{Report: r, ChartURL: csvRemember(r)}
}

// CSVPick opens the picker for a CSV file ("" when cancelled).
func (a *App) CSVPick() string {
	p, err := wr.OpenFileDialog(a.ctx, wr.OpenDialogOptions{Title: "Open a CSV file", DefaultDirectory: a.prefs.Folder,
		Filters: []wr.FileFilter{{DisplayName: "CSV files (csv, tsv, txt)", Pattern: "*.csv;*.tsv;*.txt"}}})
	if err != nil {
		return ""
	}
	return p
}

// CSVOpen opens a file a CSV report wrote with the system's default app.
func (a *App) CSVOpen(p string) string {
	csvCharts.Lock()
	ok := csvCharts.files[p]
	csvCharts.Unlock()
	if !ok {
		return "That file isn't one Blaze wrote for a CSV report."
	}
	var c *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		c = exec.Command("explorer", p)
	case "darwin":
		c = exec.Command("open", p)
	default:
		c = exec.Command("xdg-open", p)
	}
	if err := c.Start(); err != nil {
		return "Couldn't open it: " + err.Error()
	}
	go func() { _ = c.Wait() }()
	return ""
}

// csvToolText is what the agent gets back: what cleaning did, the summary table, where the
// files are and how to show the chart, plus the summary slot it may fill.
func csvToolText(r *csvkit.Report, id, chartURL string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "CSV report for %s (computed on this computer).\n\nCleaning:\n", r.Name)
	for _, n := range r.Notes {
		b.WriteString("- " + n + "\n")
	}
	b.WriteString("\nColumns: ")
	var cs []string
	for _, c := range r.Columns {
		cs = append(cs, c.Name+" ("+c.Type+")")
	}
	b.WriteString(strings.Join(cs, ", ") + "\n\n")
	s := r.Summary
	fmt.Fprintf(&b, "%s:\n", s.Label())
	for _, g := range append(append([]csvkit.Group{}, s.Groups...), s.Total) {
		if s.Value == "" {
			fmt.Fprintf(&b, "- %s: %d rows\n", g.Key, g.Rows)
			continue
		}
		fmt.Fprintf(&b, "- %s: sum %s, count %d, mean %s\n", g.Key, s.Fmt(g.Sum, "sum"), g.Count, s.Fmt(g.Mean, "mean"))
	}
	f := r.Files
	fmt.Fprintf(&b, "\nFiles written next to the source:\n- cleaned CSV: %s\n- chart: %s", f.Cleaned, f.SVG)
	if f.PNG != "" {
		fmt.Fprintf(&b, " and %s", f.PNG)
	}
	fmt.Fprintf(&b, "\n- report: %s and %s\n", f.MD, f.PDF)
	if chartURL != "" {
		fmt.Fprintf(&b, "\nTo show the chart in your reply, put this on its own line: ![%s](%s)\n", s.Label(), chartURL)
	}
	fmt.Fprintf(&b, "\nReport id: %s. Its written-summary section is still empty: write a short summary (2-5 sentences) of these numbers, using only figures above, call csv_summary with report=%q and that text so the Markdown and PDF report carry it, then give the user the same summary in your reply. Call csv_report again with group_by / value / agg (sum, count, mean) / chart (bar, line) for a different view.", id, id)
	return b.String()
}

func init() {
	tools.Register("csv_report", "Clean a CSV file and report on it, all on this computer: fixes delimiters, BOMs, quoted and ragged rows, stray spaces, blank and exact-duplicate rows, dates (to YYYY-MM-DD), numbers (currency, thousands separators) and inconsistent capitalisation; infers column types; totals a number column by a group column (sum, count, mean); draws a bar or line chart; saves the cleaned CSV, the chart (SVG + PNG) and a Markdown and PDF report next to the file. Use it for any request to clean, summarise, chart or report on a CSV/TSV.",
		[][3]string{{"path", "string", "The CSV file"}, {"group_by", "string", "Column to group by (default: the first category column)"},
			{"value", "string", "Number column to total (default: the one named like total/revenue/amount)"},
			{"agg", "string", "sum (default), count or mean"}, {"chart", "string", "bar or line (default: bar; line when grouping by a date)"}},
		[]string{"path"}, func(e *tools.Env, args map[string]any) string {
			p := tools.ArgPath(e, args, "path")
			if p == "" || p == "." {
				return "Error: give the CSV file's path."
			}
			if _, err := os.Stat(p); err != nil {
				return "Error: " + err.Error()
			}
			if e != nil && !e.Inside(p) && !e.Ask("Save a cleaned copy and a report next to this CSV?", e.Shown(filepath.Dir(p))) {
				return "The user did NOT approve writing next to that file. Ask where to save, or copy it into the project first."
			}
			if e != nil && e.Status != nil {
				e.Status("Cleaning " + filepath.Base(p))
			}
			q := CSVRequest{Path: p, GroupBy: tools.ArgS(args, "group_by"), Value: tools.ArgS(args, "value"), Agg: tools.ArgS(args, "agg"), Chart: tools.ArgS(args, "chart")}
			r, err := csvkit.Run(p, csvOptions(q))
			if err != nil {
				return "Error: " + err.Error()
			}
			id, url := csvRememberID(r)
			if a := theApp; a != nil && a.ctx != nil {
				wr.EventsEmit(a.ctx, "csv:report", CSVResult{Report: r, ChartURL: url})
			}
			return csvToolText(r, id, url)
		})
	tools.Register("csv_summary", "Put your written summary into a CSV report csv_report made (its Markdown and PDF are rewritten with it). Call it once after csv_report, with the report id it gave and 2-5 sentences that use only the figures it reported.",
		[][3]string{{"report", "string", "The report id csv_report gave"}, {"summary", "string", "Your written summary of the numbers (plain sentences, no new figures)"}},
		[]string{"report", "summary"}, func(e *tools.Env, args map[string]any) string {
			r, err := csvSetSummary(tools.ArgS(args, "report"), tools.ArgS(args, "summary"))
			if err != nil {
				return "Error: " + err.Error()
			}
			if a := theApp; a != nil && a.ctx != nil {
				wr.EventsEmit(a.ctx, "csv:summary", map[string]string{"md": r.Files.MD, "pdf": r.Files.PDF, "written": r.Written})
			}
			return fmt.Sprintf("Saved the summary into %s and %s. Now give the user the same summary in your reply.", r.Files.MD, r.Files.PDF)
		})
}
