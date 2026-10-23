package main

// Composer attachments (v0.5): the + menu, drag-and-drop and paste.
//
//   image   sent to models that can see as an image part (a downscaled copy when it is big);
//           models that can't see get the path and a note instead (internal/llm/attach.go).
//   file    text files travel inside the message (like @ files); binary files travel as a path.
//   folder  a short tree of the folder travels inside the message; Blaze reads more with its tools.

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"

	"blaze-desktop/internal/config"
	"blaze-desktop/internal/llm"
)

const (
	maxInline   = 200_000 // bytes of a text file put in the message
	maxImage    = 12 << 20
	sendSide    = 1568 // longest side of the copy sent to vision models
	thumbSide   = 160
	maxAttach   = 12
	folderLines = 300
)

var imageExt = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".bmp": true}

func attachDir(sub string) string {
	d := filepath.Join(config.Dir(), "desktop", sub)
	_ = os.MkdirAll(d, 0o700)
	return d
}

// startComposer runs at startup. Dropped files reach the composer through the Wails
// runtime's OnFileDrop in the window (real paths on every OS), then AttachPaths.
func (a *App) startComposer() {
	go cleanPasted()
}

// AttachPick opens the system picker for images, files or a folder.
func (a *App) AttachPick(kind string) []llm.Attach {
	dir := a.prefs.Folder
	switch kind {
	case "folder":
		d, err := wr.OpenDirectoryDialog(a.ctx, wr.OpenDialogOptions{Title: "Attach a folder", DefaultDirectory: dir})
		if err != nil || d == "" {
			return nil
		}
		return a.AttachPaths([]string{d})
	case "image":
		ps, err := wr.OpenMultipleFilesDialog(a.ctx, wr.OpenDialogOptions{Title: "Attach images", DefaultDirectory: dir,
			Filters: []wr.FileFilter{{DisplayName: "Images (png, jpg, gif, webp, bmp)", Pattern: "*.png;*.jpg;*.jpeg;*.gif;*.webp;*.bmp"}}})
		if err != nil {
			return nil
		}
		return a.AttachPaths(ps)
	default:
		ps, err := wr.OpenMultipleFilesDialog(a.ctx, wr.OpenDialogOptions{Title: "Attach files", DefaultDirectory: dir})
		if err != nil {
			return nil
		}
		return a.AttachPaths(ps)
	}
}

// AttachPaths describes dropped or picked paths (unreadable ones are skipped).
func (a *App) AttachPaths(paths []string) []llm.Attach {
	var out []llm.Attach
	for _, p := range paths {
		if len(out) >= maxAttach {
			break
		}
		if at, ok := describeAttach(p); ok {
			out = append(out, at)
		}
	}
	return out
}

// AttachPasted saves a pasted image (a data: URL from the clipboard) and attaches it.
func (a *App) AttachPasted(name, dataURL string) llm.Attach {
	i := strings.Index(dataURL, ";base64,")
	if !strings.HasPrefix(dataURL, "data:image/") || i < 0 {
		return llm.Attach{Note: "Only pasted images can be attached."}
	}
	b, err := base64.StdEncoding.DecodeString(dataURL[i+8:])
	if err != nil || len(b) == 0 || len(b) > maxImage {
		return llm.Attach{Note: "That image couldn't be read (or is over 12 MB)."}
	}
	ext := "." + strings.TrimPrefix(strings.SplitN(dataURL[5:i], "/", 2)[1], "x-")
	if ext == ".jpeg" {
		ext = ".jpg"
	}
	if !imageExt[ext] {
		ext = ".png"
	}
	base := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	if base == "" || base == "." || base == "image" {
		base = "pasted"
	}
	p := filepath.Join(attachDir("pasted"), time.Now().Format("20060102-150405")+"-"+safeName(base)+ext)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return llm.Attach{Note: "Couldn't save the pasted image: " + err.Error()}
	}
	at, _ := describeAttach(p)
	at.Name = "Pasted image" + ext
	return at
}

func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 128 && (r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		}
		if b.Len() >= 40 {
			break
		}
	}
	if b.Len() == 0 {
		return "image"
	}
	return b.String()
}

// cleanPasted drops pasted images and resized copies older than 30 days.
func cleanPasted() {
	for _, sub := range []string{"pasted", "attach-cache"} {
		ents, _ := os.ReadDir(attachDir(sub))
		for _, e := range ents {
			if in, err := e.Info(); err == nil && time.Since(in.ModTime()) > 30*24*time.Hour {
				_ = os.Remove(filepath.Join(attachDir(sub), e.Name()))
			}
		}
	}
}

func describeAttach(p string) (llm.Attach, bool) {
	p = filepath.Clean(strings.TrimSpace(p))
	st, err := os.Stat(p)
	if err != nil {
		return llm.Attach{}, false
	}
	at := llm.Attach{Path: p, Name: filepath.Base(p)}
	if st.IsDir() {
		at.Kind = "folder"
		at.Count = countEntries(p, 5000)
		return at, true
	}
	at.Size = st.Size()
	if imageExt[strings.ToLower(filepath.Ext(p))] {
		at.Kind = "image"
		at.Mime = llm.MimeOf(p)
		if st.Size() > maxImage*4 {
			at.Note = "too big to send"
			return at, true
		}
		prepImage(&at)
		return at, true
	}
	at.Kind = "file"
	return at, true
}

func countEntries(dir string, max int) int {
	n := 0
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && skipDir(d.Name()) && p != dir {
			return filepath.SkipDir
		}
		if p != dir {
			n++
		}
		if n >= max {
			return fs.SkipAll
		}
		return nil
	})
	return n
}

func skipDir(n string) bool {
	switch n {
	case ".git", "node_modules", ".venv", "venv", "__pycache__", "dist", "build", "target", ".next", ".cache":
		return true
	}
	return false
}

// prepImage makes the chat thumbnail and, for big pictures, a smaller copy to send.
func prepImage(at *llm.Attach) {
	f, err := os.Open(at.Path)
	if err != nil {
		return
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		// webp/bmp: no decoder in the standard library. Sent as is when small enough.
		if at.Size > maxImage {
			at.Note = "too big to send"
		}
		return
	}
	b := img.Bounds()
	if t := scaleTo(img, thumbSide); t != nil {
		var buf bytes.Buffer
		if jpeg.Encode(&buf, t, &jpeg.Options{Quality: 78}) == nil {
			at.Thumb = "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
		}
	}
	if b.Dx() <= sendSide && b.Dy() <= sendSide && at.Size <= 4<<20 {
		return
	}
	h := sha1.Sum([]byte(fmt.Sprint(at.Path, at.Size)))
	out := filepath.Join(attachDir("attach-cache"), hex.EncodeToString(h[:8])+".jpg")
	if _, err := os.Stat(out); err == nil {
		at.Send = out
		return
	}
	s := scaleTo(img, sendSide)
	if s == nil {
		return
	}
	var buf bytes.Buffer
	if jpeg.Encode(&buf, s, &jpeg.Options{Quality: 85}) == nil && os.WriteFile(out, buf.Bytes(), 0o600) == nil {
		at.Send = out
	}
}

// scaleTo shrinks img so its longest side is at most side (box filter; transparent
// pixels go on white so the JPEG copy looks right).
func scaleTo(img image.Image, side int) *image.RGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil
	}
	nw, nh := w, h
	if w >= h && w > side {
		nw, nh = side, max(1, h*side/w)
	} else if h > w && h > side {
		nw, nh = max(1, w*side/h), side
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		y0, y1 := b.Min.Y+y*h/nh, b.Min.Y+(y+1)*h/nh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < nw; x++ {
			x0, x1 := b.Min.X+x*w/nw, b.Min.X+(x+1)*w/nw
			if x1 <= x0 {
				x1 = x0 + 1
			}
			// Sample at most 4x4 points per output pixel to keep big photos fast.
			sx, sy := max(1, (x1-x0)/4), max(1, (y1-y0)/4)
			var r, g, bl, n uint32
			for yy := y0; yy < y1; yy += sy {
				for xx := x0; xx < x1; xx += sx {
					cr, cg, cb, ca := img.At(xx, yy).RGBA()
					// over white
					cr += 0xffff - ca
					cg += 0xffff - ca
					cb += 0xffff - ca
					r, g, bl, n = r+cr>>8, g+cg>>8, bl+cb>>8, n+1
				}
			}
			dst.Set(x, y, color.RGBA{uint8(min(255, r/n)), uint8(min(255, g/n)), uint8(min(255, bl/n)), 255})
		}
	}
	return dst
}

// inlineAttach puts attached files and folders into the message text and returns
// what stays on the message as attachments (all of them, for the chat's chips).
func inlineAttach(b *strings.Builder, att []llm.Attach) []llm.Attach {
	var keep []llm.Attach
	for _, at := range att {
		if at.Path == "" {
			continue
		}
		switch at.Kind {
		case "folder":
			fmt.Fprintf(b, "\n\n<attached path=\"%s\" kind=\"folder\">\n%s\n</attached>", at.Path, folderTree(at.Path))
		case "file":
			data, err := os.ReadFile(at.Path)
			switch {
			case err != nil:
				fmt.Fprintf(b, "\n\n<attached path=\"%s\" kind=\"file\">\n(couldn't read it: %v)\n</attached>", at.Path, err)
			case isText(data):
				if len(data) > maxInline {
					data = append(data[:maxInline:maxInline], []byte("\n…(cut: the file is longer; read the rest with file_read)")...)
				}
				fmt.Fprintf(b, "\n\n<attached path=\"%s\" kind=\"file\">\n%s\n</attached>", at.Path, data)
				at.Note = "inlined"
			default:
				fmt.Fprintf(b, "\n\n<attached path=\"%s\" kind=\"file\">\n(a %s binary file; it isn't text, so only its path is here. Use your tools if you need what's inside, e.g. pdftotext for a PDF.)\n</attached>", at.Path, humanSize(at.Size))
				at.Note = "path only"
			}
		case "image":
			// travels as an image part (or a path note for models that can't see)
		default:
			continue
		}
		keep = append(keep, at)
	}
	return keep
}

func isText(b []byte) bool {
	n := len(b)
	if n > 8000 {
		n = 8000
	}
	if bytes.IndexByte(b[:n], 0) >= 0 {
		return false
	}
	s := b[:n]
	for i := 0; i < 3 && len(s) > 0 && !utf8.Valid(s); i++ { // a rune cut at the sample edge
		s = s[:len(s)-1]
	}
	return utf8.Valid(s)
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}

// folderTree lists a folder (skipping .git, node_modules, build output) for the model.
func folderTree(dir string) string {
	var lines []string
	more := 0
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == dir {
			return nil
		}
		if d.IsDir() && skipDir(d.Name()) {
			return filepath.SkipDir
		}
		rel, _ := filepath.Rel(dir, p)
		if strings.Count(filepath.ToSlash(rel), "/") > 3 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if len(lines) >= folderLines {
			more++
			return nil
		}
		if d.IsDir() {
			lines = append(lines, filepath.ToSlash(rel)+"/")
		} else if in, err := d.Info(); err == nil {
			lines = append(lines, fmt.Sprintf("%s (%s)", filepath.ToSlash(rel), humanSize(in.Size())))
		}
		return nil
	})
	sort.Strings(lines)
	s := strings.Join(lines, "\n")
	if s == "" {
		s = "(empty folder)"
	}
	if more > 0 {
		s += fmt.Sprintf("\n…and %d more (list or search the folder with your tools)", more)
	}
	return s
}
