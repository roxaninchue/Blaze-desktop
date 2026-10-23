package postkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	_ "image/gif" // photos the picker accepts
	_ "image/png"

	_ "golang.org/x/image/webp" // v0.19.13: WebP photos (saved from the web, pasted, dropped)
)

// Asker sends one request to the chat model and returns its text. photo is a path on this
// computer ("" for an idea); the caller attaches it the way chat attaches images.
type Asker func(ctx context.Context, system, user, photo string) (string, error)

// ErrModel is returned when the model couldn't be reached at all (the caller shows plain words).
var ErrModel = errors.New("the model didn't answer")

// Make produces a valid kit end to end: ask, parse, assemble, validate, and retry once with the
// problems spelled out. On the last try, gaps the rules can fill (a missing hashtag, alt text or
// platform body) are filled from the hook and the idea, so a reply with usable words still gives a
// valid kit. note says when that happened.
func Make(ctx context.Context, r Request, ask Asker) (k *Kit, note string, err error) {
	if err := r.Normalize(); err != nil {
		return nil, "", err
	}
	sys, user := System(r), User(r)
	var last []string
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		u := user
		if len(last) > 0 {
			u += "\n\nYour previous reply had these problems: " + strings.Join(last, "; ") + ". Reply again with the complete JSON object only, following every rule."
		}
		text, err := ask(ctx, sys, u, r.Photo)
		if err != nil {
			if ctx.Err() != nil {
				return nil, "", ctx.Err()
			}
			lastErr = fmt.Errorf("%w: %v", ErrModel, err)
			last = nil
			continue
		}
		rp, err := ParseReply(text)
		if err != nil {
			last = []string{err.Error()}
			continue
		}
		final := attempt == 1
		if p := rp.Problems(r); len(p) > 0 && !final {
			last = p
			continue
		}
		if final {
			note = fill(r, rp)
		}
		kit := Assemble(r, rp)
		if p := Validate(kit); len(p) > 0 {
			last = p
			continue
		}
		return kit, note, nil
	}
	if lastErr != nil && last == nil {
		return nil, "", lastErr
	}
	return nil, "", fmt.Errorf("the kit didn't come out right (%s)", strings.Join(last, "; "))
}

// fill completes a reply's gaps from the hook and the idea; it says what it filled.
func fill(r Request, rp *Reply) string {
	var filled []string
	if strings.TrimSpace(stripTags(rp.Hook)) == "" {
		src := r.Idea
		if src == "" {
			src = rp.AltText
		}
		rp.Hook = firstWords(src, 10)
		filled = append(filled, "hook")
	}
	if tags := normTags(rp.Hashtags); len(tags) < 3 {
		rp.Hashtags = append(tags, wordTags(r.Idea+" "+rp.Hook+" "+rp.AltText)...)
		if len(normTags(rp.Hashtags)) < 3 {
			rp.Hashtags = append(rp.Hashtags, "#creator", "#post", "#new")
		}
		filled = append(filled, "hashtags")
	}
	if strings.TrimSpace(rp.AltText) == "" {
		rp.AltText = firstWords(stripTags(rp.Hook), 20)
		filled = append(filled, "alt text")
	}
	if rp.Posts == nil {
		rp.Posts = map[string]ReplyPost{}
	}
	for _, id := range r.Platforms {
		if p := rp.Posts[id]; strings.TrimSpace(p.Body+p.Title) == "" {
			rp.Posts[id] = ReplyPost{Title: stripTags(rp.Hook), Body: r.Idea}
			filled = append(filled, id)
		}
	}
	if len(filled) == 0 {
		return ""
	}
	return "Blaze filled in " + strings.Join(filled, ", ") + " itself. Check them before you post."
}

func firstWords(s string, n int) string {
	f := strings.Fields(s)
	if len(f) > n {
		f = f[:n]
	}
	return strings.Join(f, " ")
}

// wordTags makes hashtags from the longest distinct words of s.
func wordTags(s string) []string {
	var words []string
	for _, w := range strings.FieldsFunc(s, func(r rune) bool { return !(unicode.IsLetter(r) || unicode.IsNumber(r)) }) {
		if len([]rune(w)) >= 4 && !strings.HasPrefix(strings.ToLower(w), "http") {
			words = append(words, strings.ToLower(w))
		}
	}
	// longest first, stable
	for i := 1; i < len(words); i++ {
		for j := i; j > 0 && len([]rune(words[j])) > len([]rune(words[j-1])); j-- {
			words[j], words[j-1] = words[j-1], words[j]
		}
	}
	var out []string
	for _, w := range words {
		out = append(out, "#"+w)
	}
	return out
}

// ---------------- export ----------------

// Slug is a short file-name stem from the idea or the photo's name.
func Slug(idea, photo string) string {
	src := strings.ToLower(firstWords(idea, 5))
	if src == "" && photo != "" {
		src = strings.ToLower(strings.TrimSuffix(filepath.Base(photo), filepath.Ext(photo)))
	}
	var b strings.Builder
	dash := false
	for _, r := range src {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if rs := []rune(s); len(rs) > 40 {
		s = strings.Trim(string(rs[:40]), "-")
	}
	if s == "" {
		s = "post"
	}
	return s
}

// Export writes the kit to dir: <base>-kit.json, <base>-captions.txt and, when there is a photo,
// one JPEG per crop (<base>-4x5.jpg…), whose names are recorded in the kit's crops. The original
// photo is only read. It returns the files written and the kit as saved.
func Export(dir, base string, k *Kit, photo string) ([]string, *Kit, error) {
	if p := Validate(k); len(p) > 0 {
		return nil, nil, fmt.Errorf("the kit isn't valid: %s", strings.Join(p, "; "))
	}
	out := *k
	out.Crops = append([]Crop(nil), k.Crops...)
	var files []string
	if photo != "" {
		f, err := os.Open(photo)
		if err != nil {
			return nil, nil, err
		}
		img, _, err := image.Decode(f)
		f.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("couldn't read the photo: %v", err)
		}
		b := img.Bounds()
		if out.Source != nil && (out.Source.Width != b.Dx() || out.Source.Height != b.Dy()) {
			return nil, nil, fmt.Errorf("the photo is %dx%d but the kit's crops are for %dx%d", b.Dx(), b.Dy(), out.Source.Width, out.Source.Height)
		}
		for i, c := range out.Crops {
			rect := image.Rect(c.X, c.Y, c.X+c.W, c.Y+c.H).Add(b.Min)
			dst := image.NewRGBA(image.Rect(0, 0, c.W, c.H))
			draw.Draw(dst, dst.Bounds(), img, rect.Min, draw.Src)
			name := fmt.Sprintf("%s-%s.jpg", base, strings.ReplaceAll(c.Ratio, ":", "x"))
			fp := filepath.Join(dir, name)
			w, err := os.Create(fp)
			if err != nil {
				return files, nil, err
			}
			err = jpeg.Encode(w, dst, &jpeg.Options{Quality: 92})
			if cerr := w.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return files, nil, err
			}
			out.Crops[i].File = name
			files = append(files, fp)
		}
	}
	js, err := json.MarshalIndent(&out, "", "  ")
	if err != nil {
		return files, nil, err
	}
	kp := filepath.Join(dir, base+"-kit.json")
	if err := os.WriteFile(kp, append(js, '\n'), 0o644); err != nil {
		return files, nil, err
	}
	tp := filepath.Join(dir, base+"-captions.txt")
	if err := os.WriteFile(tp, []byte(Text(&out)), 0o644); err != nil {
		return files, nil, err
	}
	return append(files, kp, tp), &out, nil
}
