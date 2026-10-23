// Package postkit makes and checks Blaze post kits (v0.19.11): from a photo or an idea, one hook,
// three hashtags, alt text, a caption per chosen platform and crop rectangles, in exactly the
// client-neutral shape of blaze.postkit/1 (postkit.schema.json, the copy Blaze Site publishes at
// blazeinchue.pages.dev/schemas/postkit-1.json). Desktop, Mobile, CLI and the gateway post-kit
// tool all produce this shape. Kits are copy-out only: nothing here ever posts.
//
// The model writes the words (hook, hashtags, alt text, one body per platform, and where the
// subject sits in the photo). Everything with a hard rule is done here, deterministically: the
// captions are assembled and trimmed to each platform's limit at a sentence or word boundary,
// every link, @handle, email, {placeholder} and `code` from the idea is kept, hashtags are
// normalised, the crops are computed on this computer, and the result is validated before it's
// offered. A reply that can't be made valid is retried once with the problems spelled out.
package postkit

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SchemaID is the value of every kit's "schema" field.
const SchemaID = "blaze.postkit/1"

// SchemaJSON is the published JSON Schema the validator follows (kept byte-identical to Site's).
//
//go:embed postkit.schema.json
var SchemaJSON []byte

// MaxIdea is the longest idea a kit keeps (schema maxLength).
const MaxIdea = 2000

// Kit is one post kit.
type Kit struct {
	Schema    string          `json:"schema"`
	Language  string          `json:"language"`
	Idea      string          `json:"idea,omitempty"`
	Hook      string          `json:"hook"`
	Hashtags  []string        `json:"hashtags"`
	AltText   string          `json:"alt_text"`
	Platforms map[string]Post `json:"platforms"`
	Crops     []Crop          `json:"crops"`
	Source    *Source         `json:"source,omitempty"`
}

// Post is what one platform gets.
type Post struct {
	Title       string `json:"title,omitempty"`
	Caption     string `json:"caption"`
	Description string `json:"description,omitempty"`
}

// Crop is a rectangle in source-image pixels; the original image is never modified.
type Crop struct {
	Ratio string `json:"ratio"`
	X     int    `json:"x"`
	Y     int    `json:"y"`
	W     int    `json:"w"`
	H     int    `json:"h"`
	File  string `json:"file,omitempty"`
}

// Source is the picture the crops refer to.
type Source struct {
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
	Focus  *Focus `json:"focus,omitempty"`
}

// Focus is the subject centre as fractions of width and height.
type Focus struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Platform describes one platform the creator can choose.
type Platform struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Limit  int    `json:"limit"`            // caption limit (YouTube Shorts: the title)
	Title  int    `json:"title,omitempty"`  // title limit where the title is separate (Reddit)
	Inline bool   `json:"inline,omitempty"` // hashtags go inside the caption
	Hook   bool   `json:"hook,omitempty"`   // the caption opens with the shared hook
}

// Platforms in the order the picker shows them (limits from x-blaze-rules).
var Platforms = []Platform{
	{ID: "x", Name: "X", Limit: 280, Inline: true, Hook: true},
	{ID: "instagram", Name: "Instagram", Limit: 2200, Inline: true, Hook: true},
	{ID: "tiktok", Name: "TikTok", Limit: 4000, Inline: true, Hook: true},
	{ID: "linkedin", Name: "LinkedIn", Limit: 3000, Inline: true, Hook: true},
	{ID: "threads", Name: "Threads", Limit: 500, Inline: true, Hook: true},
	{ID: "youtube_shorts", Name: "YouTube Shorts", Limit: 100, Inline: true},
	{ID: "reddit", Name: "Reddit", Limit: 40000, Title: 300},
}

// DescriptionLimit is the most a description may be (YouTube Shorts).
const DescriptionLimit = 5000

// Ratios the crops come in. 16:9 is optional in the schema; Desktop always adds it.
var Ratios = []string{"1:1", "4:5", "9:16", "16:9"}

// Language is one choice in the language menu (any BCP 47 tag is accepted).
type Language struct {
	Tag  string `json:"tag"`
	Name string `json:"name"`
}

// Languages the picker offers first.
var Languages = []Language{{"en", "English"}, {"fr", "French"}, {"es", "Spanish"}, {"pt", "Portuguese"}, {"sw", "Swahili"},
	{"bem", "Bemba"}, {"ny", "Nyanja"}, {"ar", "Arabic"}, {"hi", "Hindi"}, {"zh", "Chinese"}, {"de", "German"}}

// IdeaFrame is the frame an idea-only kit's crops are planned on (a vertical 1080x1920 shot).
var IdeaFrame = Source{Width: 1080, Height: 1920, Focus: &Focus{X: 0.5, Y: 0.5}}

// PlatformByID finds a platform.
func PlatformByID(id string) (Platform, bool) {
	for _, p := range Platforms {
		if p.ID == id {
			return p, true
		}
	}
	return Platform{}, false
}

// ---------------- lengths and protected tokens ----------------

var (
	urlRe     = regexp.MustCompile(`https?://[^\s<>"'\])]+`)
	protectRe = regexp.MustCompile("https?://[^\\s<>\"'\\])]+|[\\w.+-]+@[\\w-]+(?:\\.[\\w-]+)+|(?:^|[^\\w@])(@[A-Za-z0-9_](?:[A-Za-z0-9_.]{0,28}[A-Za-z0-9_])?)|\\{\\{[^{}\\n]{1,40}\\}\\}|\\{[A-Za-z_][\\w.]{0,40}\\}|`[^`\\n]{1,80}`")
	langRe    = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)
	tagRe     = regexp.MustCompile(`^#[\p{L}\p{N}_]{1,50}$`)
	inlineTag = regexp.MustCompile(`(^|\s)#[\p{L}\p{N}_]+`)
)

// Len counts Unicode code points.
func Len(s string) int { return utf8.RuneCountInString(s) }

// XLen counts like X: every URL is 23 characters (t.co), everything else by code point.
func XLen(s string) int {
	n := 0
	last := 0
	for _, m := range urlRe.FindAllStringIndex(s, -1) {
		n += Len(s[last:m[0]]) + 23
		last = m[1]
	}
	return n + Len(s[last:])
}

func lenFor(id string) func(string) int {
	if id == "x" {
		return XLen
	}
	return Len
}

type span struct{ a, b int }

// tokenSpans are the byte ranges of protected tokens in s.
func tokenSpans(s string) []span {
	var out []span
	for _, m := range protectRe.FindAllStringSubmatchIndex(s, -1) {
		a, b := m[0], m[1]
		if m[2] >= 0 { // @handle: the group, without the character before it
			a, b = m[2], m[3]
		}
		for b > a && strings.ContainsRune(".,;:!?", rune(s[b-1])) && s[a] != '`' && s[a] != '{' {
			b--
		}
		out = append(out, span{a, b})
	}
	return out
}

// Tokens lists the protected items in text (links, emails, @handles, {placeholders}, `code`),
// each once, in order.
func Tokens(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, sp := range tokenSpans(text) {
		t := text[sp.a:sp.b]
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// Trim cuts s so length(s) <= limit, at a sentence end if one is close enough, otherwise at a word
// boundary, never inside a protected token. A string already within the limit is returned as is.
func Trim(s string, limit int, length func(string) int) string {
	if length == nil {
		length = Len
	}
	if length(s) <= limit {
		return s
	}
	if limit <= 0 {
		return ""
	}
	spans := tokenSpans(s)
	inside := func(i int) bool {
		for _, sp := range spans {
			if i > sp.a && i < sp.b {
				return true
			}
		}
		return false
	}
	// Rune starts after the first rune are the candidate cuts s[:i]. length only grows as the
	// prefix grows (an X link counts 23 from its first character after "//"), so the longest
	// prefix within the limit is found by binary search.
	var idx []int
	for i := range s {
		if i > 0 {
			idx = append(idx, i)
		}
	}
	n := sort.Search(len(idx), func(j int) bool { return length(s[:idx[j]]) > limit })
	cands := idx[:n]
	word, sentence, hard := -1, -1, -1
	for j := len(cands) - 1; j >= 0; j-- {
		i := cands[j]
		if inside(i) {
			continue
		}
		if hard < 0 {
			hard = i
		}
		prev, _ := utf8.DecodeLastRuneInString(s[:i])
		r, _ := utf8.DecodeRuneInString(s[i:])
		if !unicode.IsSpace(r) || unicode.IsSpace(prev) {
			continue
		}
		if word < 0 {
			word = i
		}
		if strings.ContainsRune(".!?…。！？", prev) {
			sentence = i
			break
		}
		if (j+1)*10 < limit*6 { // a sentence end only wins in the last 40%
			break
		}
	}
	cut := word
	if sentence > 0 {
		cut = sentence
	}
	if cut <= 0 {
		cut = hard
	}
	if cut <= 0 {
		return ""
	}
	return strings.TrimRightFunc(s[:cut], func(r rune) bool {
		return unicode.IsSpace(r) || r == ',' || r == ';' || r == ':' || r == '-' || r == '—'
	})
}

// ---------------- validation ----------------

// Validate checks a kit against blaze.postkit/1 and its x-blaze-rules. It returns every problem
// found, in plain words ("platforms.x.caption is 301 characters, over 280"); none means valid.
func Validate(k *Kit) []string {
	var p []string
	add := func(f string, a ...any) { p = append(p, fmt.Sprintf(f, a...)) }
	if k == nil {
		return []string{"no kit"}
	}
	if k.Schema != SchemaID {
		add("schema must be %q", SchemaID)
	}
	if !langRe.MatchString(k.Language) {
		add("language %q isn't a BCP 47 tag", k.Language)
	}
	if Len(k.Idea) > MaxIdea {
		add("idea is %d characters, over %d", Len(k.Idea), MaxIdea)
	}
	switch {
	case Len(k.Hook) < 1:
		add("hook is empty")
	case Len(k.Hook) > 120:
		add("hook is %d characters, over 120", Len(k.Hook))
	}
	if n := len(strings.Fields(k.Hook)); n > 12 {
		add("hook has %d words, over 12", n)
	}
	if inlineTag.MatchString(k.Hook) {
		add("hook has a hashtag")
	}
	if len(k.Hashtags) != 3 {
		add("hashtags must be exactly 3 (got %d)", len(k.Hashtags))
	}
	seen := map[string]bool{}
	for _, t := range k.Hashtags {
		if !tagRe.MatchString(t) {
			add("hashtag %q must be # then letters, digits or _ (at most 50)", t)
		}
		if seen[t] {
			add("hashtag %q appears twice", t)
		}
		seen[t] = true
	}
	switch {
	case Len(k.AltText) < 1:
		add("alt_text is empty")
	case Len(k.AltText) > 125:
		add("alt_text is %d characters, over 125", Len(k.AltText))
	}
	if len(k.Platforms) < 1 {
		add("platforms needs at least one platform")
	}
	toks := Tokens(k.Idea)
	ids := make([]string, 0, len(k.Platforms))
	for id := range k.Platforms {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		post := k.Platforms[id]
		pl, ok := PlatformByID(id)
		if !ok {
			add("platforms.%s isn't a known platform", id)
			continue
		}
		length := lenFor(id)
		if Len(post.Caption) < 1 {
			add("platforms.%s.caption is empty", id)
		} else if n := length(post.Caption); n > pl.Limit {
			add("platforms.%s.caption is %d characters, over %d", id, n, pl.Limit)
		}
		if Len(post.Title) > 300 {
			add("platforms.%s.title is %d characters, over 300", id, Len(post.Title))
		}
		if pl.Title > 0 && Len(post.Title) > pl.Title {
			add("platforms.%s.title is over %d", id, pl.Title)
		}
		if Len(post.Description) > DescriptionLimit {
			add("platforms.%s.description is %d characters, over %d", id, Len(post.Description), DescriptionLimit)
		}
		for _, t := range toks {
			if !strings.Contains(post.Caption, t) && length(post.Caption)+1+length(t) <= pl.Limit {
				add("platforms.%s.caption dropped %s", id, t)
			}
		}
	}
	if len(k.Crops) < 1 {
		add("crops needs at least one crop")
	}
	for i, c := range k.Crops {
		rw, rh, ok := ratio(c.Ratio)
		if !ok {
			add("crops[%d].ratio %q must be one of 1:1, 4:5, 9:16, 16:9", i, c.Ratio)
		}
		if c.X < 0 || c.Y < 0 || c.W < 1 || c.H < 1 {
			add("crops[%d] needs x, y >= 0 and w, h >= 1", i)
			continue
		}
		if ok && abs(c.W*rh-c.H*rw) > max(rw, rh) {
			add("crops[%d] is %dx%d, not %s", i, c.W, c.H, c.Ratio)
		}
		if s := k.Source; s != nil && s.Width > 0 && s.Height > 0 && (c.X+c.W > s.Width || c.Y+c.H > s.Height) {
			add("crops[%d] goes outside the %dx%d source", i, s.Width, s.Height)
		}
	}
	if s := k.Source; s != nil {
		if s.Width < 0 || s.Height < 0 || (s.Width == 0) != (s.Height == 0) {
			add("source width and height must both be >= 1")
		}
		if f := s.Focus; f != nil && (f.X < 0 || f.X > 1 || f.Y < 0 || f.Y > 1 || math.IsNaN(f.X) || math.IsNaN(f.Y)) {
			add("source.focus must be between 0 and 1")
		}
	}
	return p
}

// Parse reads a kit from JSON strictly: unknown fields and missing required fields are problems,
// like the schema's additionalProperties false and required. The kit is returned with the
// problems from Validate as well.
func Parse(b []byte) (*Kit, []string) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, []string{"not a JSON object: " + err.Error()}
	}
	var p []string
	for _, f := range []string{"schema", "language", "hook", "hashtags", "alt_text", "platforms", "crops"} {
		if _, ok := raw[f]; !ok {
			p = append(p, f+" is missing")
		}
	}
	if pl, ok := raw["platforms"]; ok {
		var m map[string]map[string]json.RawMessage
		if json.Unmarshal(pl, &m) == nil {
			for id, post := range m {
				if _, ok := post["caption"]; !ok {
					p = append(p, "platforms."+id+".caption is missing")
				}
			}
		}
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var k Kit
	if err := dec.Decode(&k); err != nil {
		return nil, append(p, "doesn't match blaze.postkit/1: "+err.Error())
	}
	return &k, append(p, Validate(&k)...)
}

func ratio(r string) (int, int, bool) {
	switch r {
	case "1:1":
		return 1, 1, true
	case "4:5":
		return 4, 5, true
	case "9:16":
		return 9, 16, true
	case "16:9":
		return 16, 9, true
	}
	return 0, 0, false
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// ---------------- crops ----------------

// Crops computes the largest rectangle of each ratio inside a w x h picture, centred on the focus
// where it can be (clamped to the edges).
func Crops(w, h int, f *Focus) []Crop {
	fx, fy := 0.5, 0.5
	if f != nil {
		fx, fy = clamp01(f.X), clamp01(f.Y)
	}
	var out []Crop
	for _, r := range Ratios {
		rw, rh, _ := ratio(r)
		cw, ch := w, w*rh/rw
		if ch > h {
			ch, cw = h, h*rw/rh
		}
		cw, ch = max(1, cw), max(1, ch)
		x := int(math.Round(fx*float64(w) - float64(cw)/2))
		y := int(math.Round(fy*float64(h) - float64(ch)/2))
		x = min(max(0, x), w-cw)
		y = min(max(0, y), h-ch)
		out = append(out, Crop{Ratio: r, X: x, Y: y, W: cw, H: ch})
	}
	return out
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) {
		return 0.5
	}
	return math.Min(1, math.Max(0, v))
}

// ---------------- the request, the prompt and the model's reply ----------------

// Request is what the creator gave.
type Request struct {
	Idea      string   `json:"idea"`
	Photo     string   `json:"photo,omitempty"` // a path on this computer
	Width     int      `json:"width,omitempty"` // the photo's size (read by the caller)
	Height    int      `json:"height,omitempty"`
	Language  string   `json:"language"`
	Platforms []string `json:"platforms"`
}

// Normalize fills defaults and checks a request.
func (r *Request) Normalize() error {
	r.Idea = strings.TrimSpace(r.Idea)
	if Len(r.Idea) > MaxIdea {
		return fmt.Errorf("keep the idea under %d characters", MaxIdea)
	}
	if r.Idea == "" && r.Photo == "" {
		return errors.New("add a photo or type an idea")
	}
	if r.Photo != "" && (r.Width < 1 || r.Height < 1) {
		return errors.New("that photo couldn't be read")
	}
	r.Language = strings.TrimSpace(r.Language)
	if r.Language == "" {
		r.Language = "en"
	}
	if !langRe.MatchString(r.Language) {
		return fmt.Errorf("%q isn't a language tag (like en, fr or sw)", r.Language)
	}
	var ps []string
	seen := map[string]bool{}
	for _, id := range r.Platforms {
		if _, ok := PlatformByID(id); ok && !seen[id] {
			seen[id] = true
			ps = append(ps, id)
		}
	}
	if len(ps) == 0 {
		return errors.New("choose at least one platform")
	}
	r.Platforms = ps
	return nil
}

func languageName(tag string) string {
	for _, l := range Languages {
		if strings.EqualFold(l.Tag, tag) {
			return l.Name + " (" + l.Tag + ")"
		}
	}
	return "the language with BCP 47 tag " + tag
}

// System is the instruction sent with the request.
func System(r Request) string {
	var b strings.Builder
	b.WriteString("You write social media post kits for creators. Reply with ONE JSON object and nothing else: no Markdown, no code fences, no comments.\n")
	b.WriteString("Shape: {\"hook\": string, \"hashtags\": [string, string, string], \"alt_text\": string, ")
	if r.Photo != "" {
		b.WriteString("\"focus\": {\"x\": number, \"y\": number}, ")
	}
	b.WriteString("\"posts\": {")
	for i, id := range r.Platforms {
		if i > 0 {
			b.WriteString(", ")
		}
		if id == "reddit" || id == "youtube_shorts" {
			fmt.Fprintf(&b, "%q: {\"title\": string, \"body\": string}", id)
		} else {
			fmt.Fprintf(&b, "%q: {\"body\": string}", id)
		}
	}
	b.WriteString("}}\nRules:\n")
	fmt.Fprintf(&b, "- Write every text field in %s.\n", languageName(r.Language))
	b.WriteString("- hook: the opening line every platform shares, at most 12 words, no hashtags, no emoji spam. Make people stop scrolling.\n")
	b.WriteString("- hashtags: exactly 3, specific to this post, each a # then letters, digits or _ only (no spaces, no punctuation).\n")
	if r.Photo != "" {
		b.WriteString("- alt_text: describe the photo for someone who can't see it, at most 125 characters. Don't start with \"Image of\".\n")
		b.WriteString("- focus: where the main subject's centre is in the photo, as fractions of width (x) and height (y), 0 to 1.\n")
	} else {
		b.WriteString("- alt_text: describe the picture or video the creator should post with this idea, at most 125 characters.\n")
	}
	b.WriteString("- posts: one entry for each platform listed, written in that platform's native style. body is what follows the hook; don't repeat the hook and don't put the hashtags in it.\n")
	for _, id := range r.Platforms {
		pl, _ := PlatformByID(id)
		switch id {
		case "x":
			b.WriteString("  - x: body under 180 characters (the hook and hashtags share the 280).\n")
		case "threads":
			b.WriteString("  - threads: body under 350 characters, conversational.\n")
		case "youtube_shorts":
			b.WriteString("  - youtube_shorts: title at most 70 characters; body is the description, 1-3 short sentences.\n")
		case "reddit":
			b.WriteString("  - reddit: title at most 200 characters, honest and specific, no clickbait; body is the post text, no hashtags, no marketing tone.\n")
		case "linkedin":
			b.WriteString("  - linkedin: body of 2-4 short paragraphs, professional but human.\n")
		default:
			fmt.Fprintf(&b, "  - %s: body well under %d characters.\n", id, pl.Limit)
		}
	}
	b.WriteString("- Keep every URL, @handle, email, {placeholder}, {{placeholder}} and `code` from the creator's idea exactly as written in every body; never shorten or rewrite them.\n")
	b.WriteString("- Never invent facts, prices, dates or claims that aren't in the idea or visible in the photo.")
	return b.String()
}

// User is the creator's message.
func User(r Request) string {
	var b strings.Builder
	if r.Photo != "" {
		fmt.Fprintf(&b, "Make a post kit for the attached photo (%dx%d).", r.Width, r.Height)
	} else {
		b.WriteString("Make a post kit for this idea (there's no photo yet).")
	}
	if r.Idea != "" {
		b.WriteString("\nThe creator's idea, kept as given:\n<<<\n" + r.Idea + "\n>>>")
	}
	b.WriteString("\nPlatforms: " + strings.Join(r.Platforms, ", ") + ".")
	return b.String()
}

// Reply is the model's JSON.
type Reply struct {
	Hook     string               `json:"hook"`
	Hashtags []string             `json:"hashtags"`
	AltText  string               `json:"alt_text"`
	Focus    *Focus               `json:"focus"`
	Posts    map[string]ReplyPost `json:"posts"`
}

// ReplyPost is one platform's words from the model.
type ReplyPost struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

var fenceRe = regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)```")

// ParseReply finds the JSON object in a model reply (tolerating fences and chatter around it).
func ParseReply(s string) (*Reply, error) {
	s = strings.TrimSpace(s)
	if m := fenceRe.FindStringSubmatch(s); m != nil {
		s = m[1]
	}
	i, j := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if i < 0 || j <= i {
		return nil, errors.New("the reply had no JSON object")
	}
	var r Reply
	if err := json.Unmarshal([]byte(s[i:j+1]), &r); err != nil {
		return nil, fmt.Errorf("the reply wasn't valid JSON (%v)", err)
	}
	return &r, nil
}

// Problems lists what's wrong with a reply that assembling can't fix (sent back to the model).
func (rp *Reply) Problems(r Request) []string {
	var p []string
	if strings.TrimSpace(stripTags(rp.Hook)) == "" {
		p = append(p, "hook is missing")
	}
	if len(normTags(rp.Hashtags)) < 3 {
		p = append(p, "hashtags must be 3 different hashtags of letters, digits or _")
	}
	if strings.TrimSpace(rp.AltText) == "" {
		p = append(p, "alt_text is missing")
	}
	for _, id := range r.Platforms {
		post, ok := rp.Posts[id]
		if !ok || strings.TrimSpace(post.Body+post.Title) == "" {
			p = append(p, "posts."+id+" is missing")
		}
	}
	return p
}

func stripTags(s string) string {
	return strings.Join(strings.Fields(inlineTag.ReplaceAllString(s, "$1")), " ")
}

// normTags makes model hashtags valid: one #, letters, digits and _ only, at most 50, no repeats.
func normTags(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range in {
		var b strings.Builder
		for _, r := range t {
			if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' {
				b.WriteRune(r)
			}
		}
		w := b.String()
		if rs := []rune(w); len(rs) > 50 {
			w = string(rs[:50])
		}
		if w == "" || seen[strings.ToLower(w)] {
			continue
		}
		seen[strings.ToLower(w)] = true
		out = append(out, "#"+w)
		if len(out) == 3 {
			break
		}
	}
	return out
}

// Assemble builds the kit from a reply: the words from the model, and everything with a rule
// (lengths, hashtags, preserved tokens, captions per platform, crops) from here.
func Assemble(r Request, rp *Reply) *Kit {
	k := &Kit{Schema: SchemaID, Language: r.Language, Idea: r.Idea, Platforms: map[string]Post{}}
	hook := strings.SplitN(strings.TrimSpace(stripTags(rp.Hook)), "\n", 2)[0]
	hook = strings.Trim(hook, `"“”`)
	if f := strings.Fields(hook); len(f) > 12 {
		hook = strings.Join(f[:12], " ")
	}
	k.Hook = Trim(hook, 120, Len)
	k.Hashtags = normTags(rp.Hashtags)
	alt := strings.Join(strings.Fields(rp.AltText), " ")
	k.AltText = Trim(alt, 125, Len)
	tags := strings.Join(k.Hashtags, " ")
	toks := Tokens(r.Idea)
	for _, id := range r.Platforms {
		pl, _ := PlatformByID(id)
		post := rp.Posts[id]
		body := strings.TrimSpace(post.Body)
		if strings.HasPrefix(body, k.Hook) {
			body = strings.TrimSpace(body[len(k.Hook):])
		}
		body = strings.TrimSpace(inlineTagTail(body))
		length := lenFor(id)
		var out Post
		switch id {
		case "reddit":
			title := strings.TrimSpace(post.Title)
			if title == "" {
				title = k.Hook
			}
			out.Title = Trim(title, pl.Title, Len)
			if body == "" {
				body = k.Hook
			}
			out.Caption = compose("", body, toks, "", pl.Limit, length)
		case "youtube_shorts":
			title := strings.TrimSpace(post.Title)
			if title == "" {
				title = k.Hook
			}
			title = stripTags(title)
			out.Caption = compose("", title, toks, tags, pl.Limit, length)
			if body != "" {
				out.Description = compose("", body, toks, tags, DescriptionLimit, Len)
			}
		default:
			out.Caption = compose(k.Hook, body, toks, tags, pl.Limit, length)
		}
		k.Platforms[id] = out
	}
	if r.Photo != "" {
		f := rp.Focus
		if f == nil {
			f = &Focus{X: 0.5, Y: 0.45}
		}
		f = &Focus{X: round3(clamp01(f.X)), Y: round3(clamp01(f.Y))}
		k.Source = &Source{Width: r.Width, Height: r.Height, Focus: f}
	} else {
		fr := IdeaFrame
		k.Source = &fr
	}
	k.Crops = Crops(k.Source.Width, k.Source.Height, k.Source.Focus)
	return k
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// inlineTagTail drops a trailing line of hashtags the model added despite the rules.
func inlineTagTail(s string) string {
	lines := strings.Split(s, "\n")
	for len(lines) > 0 {
		last := strings.TrimSpace(lines[len(lines)-1])
		if last == "" || strings.TrimSpace(inlineTag.ReplaceAllString(last, "")) == "" {
			lines = lines[:len(lines)-1]
			continue
		}
		break
	}
	return strings.Join(lines, "\n")
}

func missing(toks []string, s string) []string {
	var out []string
	for _, t := range toks {
		if !strings.Contains(s, t) {
			out = append(out, t)
		}
	}
	return out
}

// compose joins head, body, the protected tokens the text would lack, and the hashtags into a
// caption within limit. Room is reserved for every token the head lacks, the body is trimmed into
// what's left, and only the tokens the trimmed body lost are added back. When there's no room,
// the hashtags go first, then the tokens.
func compose(head, body string, toks []string, tags string, limit int, length func(string) int) string {
	join := func(parts ...string) string {
		var keep []string
		for _, p := range parts {
			if strings.TrimSpace(p) != "" {
				keep = append(keep, strings.TrimSpace(p))
			}
		}
		return strings.Join(keep, "\n\n")
	}
	reserve := missing(toks, head)
	for _, t := range []string{tags, ""} {
		for _, res := range [][]string{reserve, nil} {
			fixed := join(head, strings.Join(res, " "), t)
			if length(fixed) > limit {
				continue
			}
			if strings.TrimSpace(body) == "" {
				return fixed
			}
			sep := 0
			if fixed != "" {
				sep = 2
			}
			b := Trim(body, limit-length(fixed)-sep, length)
			if strings.TrimSpace(b) == "" && t != "" {
				continue // no room for any body with the hashtags: drop them first
			}
			tl := func() string { return strings.Join(missing(res, b), " ") }
			c := join(head, joinLine(b, tl()), t)
			for length(c) > limit && b != "" { // separators changed the count: shave
				b = Trim(b, length(b)-1, length)
				c = join(head, joinLine(b, tl()), t)
			}
			if length(c) <= limit {
				return c
			}
		}
	}
	return Trim(join(head, body), limit, length)
}

func joinLine(body, toks string) string {
	if toks == "" {
		return body
	}
	if strings.TrimSpace(body) == "" {
		return toks
	}
	return strings.TrimSpace(body) + " " + toks
}

// Text is the kit as plain text for copying or the captions file.
func Text(k *Kit) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Hook: %s\nHashtags: %s\nAlt text: %s\n", k.Hook, strings.Join(k.Hashtags, " "), k.AltText)
	for _, pl := range Platforms {
		post, ok := k.Platforms[pl.ID]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "\n=== %s ===\n", pl.Name)
		if post.Title != "" {
			fmt.Fprintf(&b, "Title: %s\n\n", post.Title)
		}
		b.WriteString(post.Caption + "\n")
		if post.Description != "" {
			fmt.Fprintf(&b, "\nDescription:\n%s\n", post.Description)
		}
	}
	return b.String()
}
