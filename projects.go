package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"

	"blaze-desktop/internal/config"
	"blaze-desktop/internal/tools"
)

// A Project is a folder with its own chats and standing instructions.
type Project struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Folder       string    `json:"folder"`
	Instructions string    `json:"instructions"`
	Created      time.Time `json:"created"`
	Opened       time.Time `json:"opened"`
}

func projectsFile() string { return filepath.Join(config.Dir(), "desktop", "projects.json") }

func loadProjects() []Project {
	var ps []Project
	b, _ := os.ReadFile(projectsFile())
	_ = json.Unmarshal(b, &ps)
	return ps
}

func saveProjects(ps []Project) {
	_ = os.MkdirAll(filepath.Dir(projectsFile()), 0o700)
	b, _ := json.MarshalIndent(ps, "", "  ")
	_ = os.WriteFile(projectsFile(), b, 0o600)
}

func (a *App) Projects() []Project {
	ps := loadProjects()
	sort.Slice(ps, func(i, j int) bool { return ps[i].Opened.After(ps[j].Opened) })
	return ps
}

func (a *App) project() *Project {
	if a.prefs.Project == "" {
		return nil
	}
	for _, p := range loadProjects() {
		if p.ID == a.prefs.Project {
			p := p
			return &p
		}
	}
	return nil
}

// ChooseFolder asks for a folder without switching to it.
func (a *App) ChooseFolder() string {
	d, _ := wr.OpenDirectoryDialog(a.ctx, wr.OpenDialogOptions{Title: "Choose the project folder", DefaultDirectory: a.prefs.Folder})
	return d
}

func (a *App) CreateProject(folder, name string) (Project, error) {
	if st, err := os.Stat(folder); err != nil || !st.IsDir() {
		return Project{}, errf("That folder doesn't exist.")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = filepath.Base(folder)
	}
	ps := loadProjects()
	for _, p := range ps {
		if p.Folder == folder {
			return a.OpenProject(p.ID), nil
		}
	}
	p := Project{ID: newID(), Name: name, Folder: folder, Created: time.Now(), Opened: time.Now()}
	saveProjects(append(ps, p))
	return a.OpenProject(p.ID), nil
}

// OpenProject switches to a project ("" = General) and starts a fresh chat there.
func (a *App) OpenProject(id string) Project {
	ps := loadProjects()
	var out Project
	for i := range ps {
		if ps[i].ID == id {
			ps[i].Opened = time.Now()
			out = ps[i]
		}
	}
	saveProjects(ps)
	a.prefs.Project = out.ID
	if out.Folder != "" {
		a.SetFolder(out.Folder)
	} else {
		a.savePrefs()
	}
	a.NewConversation()
	return out
}

func (a *App) UpdateProject(id, name, instructions string) {
	ps := loadProjects()
	for i := range ps {
		if ps[i].ID == id {
			if strings.TrimSpace(name) != "" {
				ps[i].Name = strings.TrimSpace(name)
			}
			ps[i].Instructions = strings.TrimSpace(instructions)
		}
	}
	saveProjects(ps)
}

// DeleteProject forgets the project and its chats. Files on disk are never touched.
func (a *App) DeleteProject(id string) {
	ps := loadProjects()
	var keep []Project
	for _, p := range ps {
		if p.ID != id {
			keep = append(keep, p)
		}
	}
	saveProjects(keep)
	for _, c := range a.allConvs() {
		if c.Project == id {
			_ = os.Remove(filepath.Join(convDir(), c.ID+".json"))
		}
	}
	if a.prefs.Project == id {
		a.prefs.Project = ""
		a.savePrefs()
		a.NewConversation()
	}
}

type ProjectInfo struct {
	Project Project    `json:"project"`
	Branch  string     `json:"branch"`
	Status  string     `json:"status"`
	Recent  []FileItem `json:"recent"`
	Chats   []ConvItem `json:"chats"`
}

type FileItem struct {
	Path string `json:"path"` // relative to the project folder
	When string `json:"when"`
}

func (a *App) ProjectDetails(id string) ProjectInfo {
	var info ProjectInfo
	for _, p := range loadProjects() {
		if p.ID == id {
			info.Project = p
		}
	}
	dir := info.Project.Folder
	if dir == "" {
		return info
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		sh := tools.NewShell(a.info.Shell, a.info.ShellPath, dir)
		info.Branch = firstLine(stripExit(sh.Run("git rev-parse --abbrev-ref HEAD", 10*time.Second)))
		st := stripExit(sh.Run("git status --short", 10*time.Second))
		if strings.TrimSpace(st) == "" {
			st = "Clean"
		}
		info.Status = st
	}
	files := walkFiles(dir, 4000)
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	for i, f := range files {
		if i >= 12 {
			break
		}
		info.Recent = append(info.Recent, FileItem{f.rel, f.mod.Format("Jan 2 15:04")})
	}
	info.Chats = a.Conversations(id)
	return info
}

// Files lists project files for the @ picker.
func (a *App) Files() []string {
	var out []string
	for _, f := range walkFiles(a.prefs.Folder, 5000) {
		out = append(out, f.rel)
	}
	sort.Strings(out)
	return out
}

type walked struct {
	rel string
	mod time.Time
}

var skipDirs = map[string]bool{".git": true, "node_modules": true, ".venv": true, "venv": true, "__pycache__": true,
	"dist": true, "build": true, ".next": true, "target": true, ".cache": true, ".idea": true, ".gradle": true, "vendor": true}

func walkFiles(root string, max int) []walked {
	var out []walked
	if root == "" {
		return out
	}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if len(out) >= max {
			return filepath.SkipAll
		}
		if d.IsDir() {
			if p != root && (skipDirs[d.Name()] || (strings.HasPrefix(d.Name(), ".") && d.Name() != ".github")) {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		out = append(out, walked{filepath.ToSlash(rel), info.ModTime()})
		return nil
	})
	return out
}

func stripExit(s string) string {
	// Shell results start with "exit N · cwd …".
	if i := strings.Index(s, "\n"); i >= 0 && strings.HasPrefix(s, "exit ") {
		return s[i+1:]
	}
	if strings.HasPrefix(s, "exit ") {
		return ""
	}
	return s
}

func firstLine(s string) string {
	return strings.TrimSpace(strings.SplitN(strings.TrimSpace(s), "\n", 2)[0])
}

type simpleErr string

func (e simpleErr) Error() string { return string(e) }
func errf(s string) error         { return simpleErr(s) }
