package main

// Post kit (v0.19.11, creators workflow C1): give a photo or an idea, choose platforms, and get a
// full blaze.postkit/1 kit: hook, three hashtags, alt text, a caption per platform within its
// limit, and crops for 1:1, 4:5, 9:16 and 16:9. The words come from the same model as chat
// (Blaze Cloud or the user's own, with Private vision respected); the rules, the crops and the
// export happen on this computer (internal/postkit). Copy-out only: Blaze never posts.
// Nothing here names a provider or a model.

import (
	"context"
	"encoding/json"
	"image"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"
	_ "golang.org/x/image/webp" // v0.19.13: WebP photos too

	"blaze-desktop/internal/llm"
	"blaze-desktop/internal/postkit"
)

// postkitComplete is the chat model call (a variable so tests can stand in for the model).
var postkitComplete = llm.CompleteWith

// PostKitPhoto is a photo picked, dropped or pasted for a kit.
type PostKitPhoto struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Thumb  string `json:"thumb,omitempty"` // small data URL for the preview
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Error  string `json:"error,omitempty"`
}

// PostKitRequest is what the Post kit view sends.
type PostKitRequest struct {
	Idea      string   `json:"idea"`
	Photo     string   `json:"photo"`
	Language  string   `json:"language"`
	Platforms []string `json:"platforms"`
}

// PostKitResult is a kit (or why there isn't one).
type PostKitResult struct {
	Kit      *postkit.Kit `json:"kit,omitempty"`
	JSON     string       `json:"json,omitempty"` // the kit exactly as exported
	Text     string       `json:"text,omitempty"` // every caption as plain text
	Photo    string       `json:"photo,omitempty"`
	Note     string       `json:"note,omitempty"`
	Problems []string     `json:"problems,omitempty"` // an opened kit that doesn't match the schema
	Error    string       `json:"error,omitempty"`
}

// PostKitOptions is what the view offers.
func (a *App) PostKitOptions() map[string]any {
	return map[string]any{"platforms": postkit.Platforms, "languages": postkit.Languages, "ratios": postkit.Ratios,
		"default_platforms": []string{"instagram", "tiktok", "x"}}
}

// PostKitPickPhoto opens the picker for one photo.
func (a *App) PostKitPickPhoto() PostKitPhoto {
	p, err := wr.OpenFileDialog(a.ctx, wr.OpenDialogOptions{Title: "Choose a photo for the post", DefaultDirectory: a.prefs.Folder,
		Filters: []wr.FileFilter{{DisplayName: "Photos (jpg, png, gif, webp)", Pattern: "*.jpg;*.jpeg;*.png;*.gif;*.webp"}}})
	if err != nil || p == "" {
		return PostKitPhoto{}
	}
	return postKitPhoto(p)
}

// PostKitPhotoPath describes a dropped photo.
func (a *App) PostKitPhotoPath(p string) PostKitPhoto { return postKitPhoto(p) }

// PostKitPhotoPasted saves a pasted image and describes it.
func (a *App) PostKitPhotoPasted(name, dataURL string) PostKitPhoto {
	at := a.AttachPasted(name, dataURL)
	if at.Path == "" {
		return PostKitPhoto{Error: at.Note}
	}
	return postKitPhoto(at.Path)
}

func postKitPhoto(p string) PostKitPhoto {
	p = filepath.Clean(strings.TrimSpace(p))
	ph := PostKitPhoto{Path: p, Name: filepath.Base(p)}
	f, err := os.Open(p)
	if err != nil {
		ph.Error = "That photo couldn't be opened."
		return ph
	}
	cfg, _, err := image.DecodeConfig(f)
	f.Close()
	if err != nil {
		ph.Error = "Use a JPG, PNG, GIF or WebP photo."
		return ph
	}
	ph.Width, ph.Height = cfg.Width, cfg.Height
	if at, ok := describeAttach(p); ok {
		ph.Thumb = at.Thumb
	}
	return ph
}

// postKitAsker sends one kit request through the app's normal chat path. The photo is attached
// exactly like a chat image, so Private vision and the image downscaling apply.
func (a *App) postKitAsker() postkit.Asker {
	return func(ctx context.Context, system, user, photo string) (string, error) {
		um := llm.Message{Role: "user", Content: user}
		if photo != "" {
			if at, ok := describeAttach(photo); ok && at.Kind == "image" {
				um.Attach = []llm.Attach{at}
			}
		}
		r, err := postkitComplete(ctx, a.cfg, []llm.Message{{Role: "system", Content: system}, um}, nil, llm.Opts{})
		if err != nil {
			return "", err
		}
		return r.Text, nil // only the words: never the route, provider or model
	}
}

// PostKitMake makes a kit from a photo and/or an idea.
func (a *App) PostKitMake(req PostKitRequest) PostKitResult {
	r := postkit.Request{Idea: req.Idea, Photo: strings.TrimSpace(req.Photo), Language: req.Language, Platforms: req.Platforms}
	if r.Photo != "" {
		ph := postKitPhoto(r.Photo)
		if ph.Error != "" {
			return PostKitResult{Error: ph.Error}
		}
		r.Photo, r.Width, r.Height = ph.Path, ph.Width, ph.Height
	}
	if err := r.Normalize(); err != nil {
		return PostKitResult{Error: capFirst(err.Error()) + "."}
	}
	if why := a.capReached(); why != "" {
		return PostKitResult{Error: why}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	k, note, err := postkit.Make(ctx, r, a.postKitAsker())
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return PostKitResult{Error: "That took too long. Try again in a moment."}
		case strings.Contains(err.Error(), postkit.ErrModel.Error()):
			return PostKitResult{Error: "Blaze couldn't reach the model right now. Try again in a moment."}
		}
		return PostKitResult{Error: "Blaze couldn't make a kit that follows every rule this time. Try again, or add a few words to the idea."}
	}
	return postKitResult(k, r.Photo, note)
}

func postKitResult(k *postkit.Kit, photo, note string) PostKitResult {
	js, _ := json.MarshalIndent(k, "", "  ")
	return PostKitResult{Kit: k, JSON: string(js), Text: postkit.Text(k), Photo: photo, Note: note}
}

// PostKitCheck validates a kit (pasted JSON, or one from another Blaze app or the gateway tool).
func (a *App) PostKitCheck(js string) PostKitResult {
	k, problems := postkit.Parse([]byte(js))
	if k == nil {
		return PostKitResult{Problems: problems, Error: "That isn't a Blaze post kit."}
	}
	res := postKitResult(k, "", "")
	res.Problems = problems
	return res
}

// PostKitOpen opens a saved kit (<name>-kit.json) and checks it.
func (a *App) PostKitOpen() PostKitResult {
	p, err := wr.OpenFileDialog(a.ctx, wr.OpenDialogOptions{Title: "Open a post kit", DefaultDirectory: a.prefs.Folder,
		Filters: []wr.FileFilter{{DisplayName: "Post kit (*.json)", Pattern: "*.json"}}})
	if err != nil || p == "" {
		return PostKitResult{}
	}
	b, err := os.ReadFile(p)
	if err != nil || len(b) > 1<<20 {
		return PostKitResult{Error: "That file couldn't be read."}
	}
	return a.PostKitCheck(string(b))
}

// PostKitExportResult says what was saved.
type PostKitExportResult struct {
	Dir   string   `json:"dir,omitempty"`
	Files []string `json:"files,omitempty"`
	JSON  string   `json:"json,omitempty"`
	Error string   `json:"error,omitempty"`
}

// PostKitExport asks for a folder and saves the kit there: kit JSON, captions and, with a photo,
// one cropped JPEG per ratio. The original photo is never changed.
func (a *App) PostKitExport(js, photo string) PostKitExportResult {
	dir, err := wr.OpenDirectoryDialog(a.ctx, wr.OpenDialogOptions{Title: "Save the post kit in…", DefaultDirectory: a.prefs.Folder})
	if err != nil || dir == "" {
		return PostKitExportResult{}
	}
	return postKitExportTo(dir, js, photo)
}

func postKitExportTo(dir, js, photo string) PostKitExportResult {
	k, problems := postkit.Parse([]byte(js))
	if k == nil || len(problems) > 0 {
		return PostKitExportResult{Error: "This kit doesn't match blaze.postkit/1, so it wasn't saved."}
	}
	files, saved, err := postkit.Export(dir, postkit.Slug(k.Idea, photo), k, photo)
	if err != nil {
		return PostKitExportResult{Error: "Couldn't save the kit: " + err.Error()}
	}
	out, _ := json.MarshalIndent(saved, "", "  ")
	return PostKitExportResult{Dir: dir, Files: files, JSON: string(out)}
}

func capFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
