package studio

// One-click ship (v0.8.0): publish the design to Cloudflare Pages or GitHub Pages on
// their free tiers. Stage copies the static site (or runs the project's build and takes
// its output) into a clean folder and, when shared comments are on, adds the comment
// pins script to every page.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var staticExt = regexp.MustCompile(`(?i)\.(html?|css|js|mjs|json|svg|png|jpe?g|gif|webp|avif|ico|woff2?|ttf|otf|txt|xml|webmanifest|map|mp4|webm|mp3)$`)
var skipShip = map[string]bool{".git": true, "node_modules": true, ".venv": true, "venv": true, "__pycache__": true, ".cache": true, ".blaze-worktrees": true, ".github": true, ".vscode": true, ".idea": true}

// Runner runs a command in a folder and returns its combined output.
type Runner func(ctx context.Context, dir, name string, args ...string) (string, error)

// ExecRunner is the real Runner.
func ExecRunner(hide func(*exec.Cmd)) Runner {
	return func(ctx context.Context, dir, name string, args ...string) (string, error) {
		c := exec.CommandContext(ctx, name, args...)
		c.Dir = dir
		if hide != nil {
			hide(c)
		}
		c.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "CI=1", "FORCE_COLOR=0", "NO_COLOR=1")
		var b bytes.Buffer
		c.Stdout, c.Stderr = &b, &b
		err := c.Run()
		return b.String(), err
	}
}

// Stage puts the site to publish in dest. With a build script it runs it (via run)
// and copies the build output; otherwise it copies the folder's static files.
func Stage(ctx context.Context, folder, dest string, st Stack, run Runner) (int, error) {
	src := folder
	if st.Build != "" && st.Framework != "html" {
		if _, err := os.Stat(filepath.Join(folder, "node_modules")); err != nil {
			if out, err := run(ctx, folder, "npm", "install"); err != nil {
				return 0, fmt.Errorf("npm install failed: %v\n%s", err, tail(out, 1200))
			}
		}
		if out, err := run(ctx, folder, "npm", "run", "build"); err != nil {
			return 0, fmt.Errorf("the build failed: %v\n%s", err, tail(out, 1200))
		}
		src = filepath.Join(folder, filepath.FromSlash(st.Out))
		if _, err := os.Stat(src); err != nil {
			for _, alt := range []string{"dist", "build", "out", "public"} {
				if _, err := os.Stat(filepath.Join(folder, alt, "index.html")); err == nil {
					src = filepath.Join(folder, alt)
					break
				}
			}
		}
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return 0, err
	}
	n := 0
	var size int64
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			if p != src && (skipShip[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !staticExt.MatchString(d.Name()) || d.Name() == "package.json" || d.Name() == "package-lock.json" || strings.HasSuffix(d.Name(), ".config.js") {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 25<<20 {
			return nil
		}
		size += info.Size()
		if size > 200<<20 {
			return errors.New("the site is over 200 MB; that's more than the free tiers take in one go")
		}
		if err := copyOne(p, filepath.Join(dest, rel)); err != nil {
			return err
		}
		n++
		return nil
	})
	if err != nil {
		return n, err
	}
	if _, err := os.Stat(filepath.Join(dest, "index.html")); err != nil {
		return n, errors.New("there's no index.html to publish (make a page first, or check the build output folder)")
	}
	return n, nil
}

func copyOne(from, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(to)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

func tail(s string, n int) string {
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}

// CommentsTag is the script that adds pinned comments to a shared page.
func CommentsTag(endpoint, site string) string {
	return `<script src="` + strings.TrimRight(endpoint, "/") + `/v1/comments.js" data-site="` + site + `" defer></script>`
}

// InjectComments adds the comments script to every HTML file in dir.
func InjectComments(dir, endpoint, site string) error {
	tag := CommentsTag(endpoint, site)
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".html") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil || bytes.Contains(b, []byte("/v1/comments.js")) {
			return nil
		}
		s := string(b)
		if i := strings.LastIndex(strings.ToLower(s), "</body>"); i >= 0 {
			s = s[:i] + tag + s[i:]
		} else {
			s += tag
		}
		return os.WriteFile(p, []byte(s), 0o644)
	})
}

var slugRe = regexp.MustCompile(`[^a-z0-9-]+`)

// Slug makes a project name for a hosting provider (lowercase, a-z0-9-, ≤ 58 chars).
func Slug(s string) string {
	s = slugRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-")
	s = strings.Trim(regexp.MustCompile(`-+`).ReplaceAllString(s, "-"), "-")
	if len(s) > 58 {
		s = strings.Trim(s[:58], "-")
	}
	if s == "" {
		s = "blaze-site"
	}
	return s
}

var pagesDevRe = regexp.MustCompile(`https://[a-z0-9.-]+\.pages\.dev[^\s]*`)

// ShipCloudflare deploys dir to Cloudflare Pages with wrangler (via npx). The first
// time, wrangler opens the browser to sign in to Cloudflare (free account).
func ShipCloudflare(ctx context.Context, dir, project string, run Runner) (string, error) {
	w := []string{"--yes", "wrangler@4"}
	if out, err := run(ctx, dir, "npx", append(w, "whoami")...); err != nil || strings.Contains(strings.ToLower(out), "not authenticated") {
		lctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		if out, err := run(lctx, dir, "npx", append(w, "login")...); err != nil {
			return "", fmt.Errorf("couldn't sign in to Cloudflare: %v\n%s", err, tail(out, 600))
		}
	}
	_, _ = run(ctx, dir, "npx", append(w, "pages", "project", "create", project, "--production-branch", "main")...)
	out, err := run(ctx, dir, "npx", append(w, "pages", "deploy", ".", "--project-name", project, "--branch", "main", "--commit-dirty=true")...)
	if err != nil {
		return "", fmt.Errorf("wrangler pages deploy failed: %v\n%s", err, tail(out, 1200))
	}
	if m := pagesDevRe.FindAllString(out, -1); len(m) > 0 {
		// The project URL (project.pages.dev) is stabler than the per-deploy one.
		return "https://" + project + ".pages.dev", nil
	}
	return "https://" + project + ".pages.dev", nil
}

var ghRemoteRe = regexp.MustCompile(`github\.com[:/]([^/]+)/([^/\s]+?)(?:\.git)?/?$`)

// GitHubRepo reads owner and repo from a git remote URL.
func GitHubRepo(remote string) (owner, repo string, ok bool) {
	m := ghRemoteRe.FindStringSubmatch(strings.TrimSpace(remote))
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// PagesURL is where GitHub Pages serves a repo.
func PagesURL(owner, repo string) string {
	if strings.EqualFold(repo, owner+".github.io") {
		return "https://" + strings.ToLower(owner) + ".github.io/"
	}
	return "https://" + strings.ToLower(owner) + ".github.io/" + repo + "/"
}

// ShipGitHub force-pushes dir as the gh-pages branch of the project's GitHub remote
// (with the user's own git credentials) and turns Pages on when the gh CLI is there.
func ShipGitHub(ctx context.Context, folder, dir string, run Runner) (string, error) {
	remote, err := run(ctx, folder, "git", "remote", "get-url", "origin")
	if err != nil {
		return "", errors.New("this folder has no GitHub remote (git remote origin); push it to GitHub first, or ship to Cloudflare Pages")
	}
	remote = strings.TrimSpace(remote)
	owner, repo, ok := GitHubRepo(remote)
	if !ok {
		return "", fmt.Errorf("the origin remote (%s) isn't on GitHub", remote)
	}
	_ = os.WriteFile(filepath.Join(dir, ".nojekyll"), nil, 0o644)
	steps := [][]string{
		{"init", "-q"},
		{"checkout", "-q", "-b", "gh-pages"},
		{"add", "-A"},
		{"-c", "user.name=Blaze", "-c", "user.email=blaze@localhost", "commit", "-qm", "Publish with Blaze"},
		{"push", "-qf", remote, "gh-pages"},
	}
	for _, s := range steps {
		if out, err := run(ctx, dir, "git", s...); err != nil {
			return "", fmt.Errorf("git %s failed: %v\n%s", s[0], err, tail(out, 800))
		}
	}
	if _, err := exec.LookPath("gh"); err == nil {
		_, _ = run(ctx, folder, "gh", "api", "-X", "POST", "repos/"+owner+"/"+repo+"/pages", "-f", "source[branch]=gh-pages", "-f", "source[path]=/")
	}
	return PagesURL(owner, repo), nil
}
