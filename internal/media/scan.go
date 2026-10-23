package media

// The folder scanner: the user's music and video folders (~/Music, ~/Videos or
// ~/Movies, Blaze's own download folders, and any folder they add). Audio tags and
// embedded covers come from github.com/dhowden/tag (ID3v1/2, MP4, FLAC, Ogg); a
// cover.jpg / folder.jpg next to the songs is used when there's no embedded one.
// Unchanged files (same size and mtime) come from the cache, so a rescan is quick.

import (
	"crypto/sha1"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/dhowden/tag"
)

var audioExts = map[string]bool{".mp3": true, ".m4a": true, ".aac": true, ".flac": true, ".ogg": true, ".oga": true, ".opus": true, ".wav": true}
var videoExts = map[string]bool{".mp4": true, ".m4v": true, ".webm": true, ".mkv": true, ".mov": true, ".ogv": true}

// KindOf is "audio", "video" or "" for a file name.
func KindOf(name string) string {
	e := strings.ToLower(filepath.Ext(name))
	if audioExts[e] {
		return "audio"
	}
	if videoExts[e] {
		return "video"
	}
	return ""
}

// FileKey is the library key of a file.
func FileKey(path string) string { return "file:" + filepath.Clean(path) }

const maxScanFiles = 30000

var reTrackNo = regexp.MustCompile(`^\s*\d{1,3}\s*[-._)]\s*`)

// TitleFromFile: "03 - My Song.mp3" → "My Song"; "my_song" → "my song".
func TitleFromFile(name string) string {
	b := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	b = reTrackNo.ReplaceAllString(b, "")
	b = strings.TrimSpace(strings.ReplaceAll(b, "_", " "))
	if b == "" {
		return filepath.Base(name)
	}
	return b
}

// Scan rescans the folders in the background (one at a time); the UI hears
// media:library when it's done.
func (m *Media) Scan() {
	m.mu.Lock()
	if m.scanning {
		m.mu.Unlock()
		return
	}
	m.scanning = true
	m.mu.Unlock()
	m.emit("media:scan", true)
	go func() {
		out := m.ScanNow()
		m.mu.Lock()
		m.scanned = out
		m.scanning = false
		m.lastScan = m.now()
		m.writeJSON("scan.json", m.scanned)
		// Downloads the user deleted elsewhere leave the library.
		kept := []Track{}
		for _, t := range m.downloads {
			if _, err := os.Stat(t.Path); err == nil {
				kept = append(kept, t)
			}
		}
		if len(kept) != len(m.downloads) {
			m.downloads = kept
			m.writeJSON("library.json", m.downloads)
		}
		m.mu.Unlock()
		m.emit("media:scan", false)
		m.changed()
	}()
}

// ScanNow walks the folders and returns every audio/video file as a Track.
func (m *Media) ScanNow() []Track {
	folders := m.Folders()
	m.mu.Lock()
	cache := map[string]Track{}
	for _, t := range m.scanned {
		cache[t.Key] = t
	}
	m.mu.Unlock()
	artDir := filepath.Join(m.dir, "art")
	_ = os.MkdirAll(artDir, 0o755)
	out := []Track{}
	seen := map[string]bool{}
	for _, root := range folders {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			name := d.Name()
			if d.IsDir() {
				if p != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "$RECYCLE.BIN") {
					return fs.SkipDir
				}
				if strings.Count(strings.TrimPrefix(p, root), string(os.PathSeparator)) > 8 {
					return fs.SkipDir
				}
				return nil
			}
			kind := KindOf(name)
			if kind == "" || strings.HasPrefix(name, ".") || len(out) >= maxScanFiles {
				return nil
			}
			key := FileKey(p)
			if seen[key] {
				return nil
			}
			seen[key] = true
			info, err := d.Info()
			if err != nil || info.Size() < 1024 {
				return nil
			}
			mt := info.ModTime().UnixMilli()
			if c, ok := cache[key]; ok && c.Size == info.Size() && c.MTime == mt {
				out = append(out, c)
				return nil
			}
			out = append(out, readTrack(p, kind, info.Size(), mt, artDir))
			return nil
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].AddedAt > out[j].AddedAt })
	return out
}

// readTrack builds a Track from a file: tags (audio and mp4), cover, title fallback.
func readTrack(p, kind string, size, mtime int64, artDir string) Track {
	t := Track{Key: FileKey(p), Path: p, Title: TitleFromFile(p), Source: "folder", AddedAt: mtime, Kind: kind, Size: size, MTime: mtime}
	if f, err := os.Open(p); err == nil {
		md, err := tag.ReadFrom(f)
		f.Close()
		if err == nil && md != nil {
			if s := strings.TrimSpace(md.Title()); s != "" {
				t.Title = s
			}
			t.Artist = strings.TrimSpace(orStr(md.Artist(), md.AlbumArtist()))
			t.Album = strings.TrimSpace(md.Album())
			t.Genre = strings.TrimSpace(md.Genre())
			t.Year = md.Year()
			t.TrackNo, _ = md.Track()
			if pic := md.Picture(); pic != nil && len(pic.Data) > 200 {
				h := sha1.Sum(pic.Data)
				ext := ".jpg"
				if strings.Contains(strings.ToLower(pic.MIMEType), "png") {
					ext = ".png"
				}
				af := filepath.Join(artDir, "tag_"+hex.EncodeToString(h[:10])+ext)
				if _, err := os.Stat(af); err != nil {
					_ = os.WriteFile(af, pic.Data, 0o644)
				}
				t.Art = af
			}
		}
	}
	if t.Art == "" {
		t.Art = folderCover(filepath.Dir(p), kind, p)
	}
	return t
}

// folderCover is cover.jpg / folder.jpg (audio) or "<video>.jpg" / poster.jpg (video).
func folderCover(dir, kind, file string) string {
	var names []string
	if kind == "video" {
		b := strings.TrimSuffix(file, filepath.Ext(file))
		for _, e := range []string{".jpg", ".png", "-poster.jpg", ".webp"} {
			if _, err := os.Stat(b + e); err == nil {
				return b + e
			}
		}
		names = []string{"poster.jpg", "folder.jpg"}
	} else {
		names = []string{"cover.jpg", "cover.png", "folder.jpg", "folder.png", "front.jpg", "album.jpg", "Cover.jpg", "Folder.jpg", "AlbumArtSmall.jpg"}
	}
	for _, n := range names {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			return filepath.Join(dir, n)
		}
	}
	return ""
}

// SetFolders replaces the extra folders (and whether the defaults are scanned), then rescans.
func (m *Media) SetFolders(folders []string, noDefaults bool) {
	var clean []string
	for _, f := range folders {
		f = strings.TrimSpace(f)
		if f != "" {
			clean = append(clean, filepath.Clean(f))
		}
	}
	m.UpdatePrefs(func(p *Prefs) { p.Folders, p.NoDefaults = clean, noDefaults })
	m.Scan()
}
