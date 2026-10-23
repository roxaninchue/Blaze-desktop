package media

// The Store as the views use it: search a source (a pasted link goes to "Download
// by link"), open an item (qualities, licence, what playing it costs), play it
// without downloading (through the stream proxy) or download it.

import "strings"

// StoreResult is one search answer.
type StoreResult struct {
	Page
	Error string `json:"error"`
	Setup string `json:"setup"`
	Link  bool   `json:"link"`
}

// StoreSearch searches source for kind ("" query = browse when the source can).
func (m *Media) StoreSearch(kind, source, query, next string) StoreResult {
	q := strings.TrimSpace(query)
	if u := FirstURL(q); u != "" && (strings.Contains(q, "://") || YoutubeID(u) != "") {
		it, err := ResolveLink(q)
		if err != nil {
			return StoreResult{Error: err.Error(), Page: Page{Items: []Item{}}}
		}
		return StoreResult{Page: Page{Items: []Item{it}}, Link: true}
	}
	s := m.Source(source)
	if s == nil {
		return StoreResult{Error: "Pick a source.", Page: Page{Items: []Item{}}}
	}
	if su := s.SetupNeeded(); su != "" {
		return StoreResult{Setup: su, Page: Page{Items: []Item{}}}
	}
	if q == "" && next == "" && !s.Browsable() {
		return StoreResult{Page: Page{Items: []Item{}}}
	}
	p, err := s.Search(kind, q, next)
	if err != nil {
		return StoreResult{Error: err.Error(), Page: Page{Items: []Item{}}}
	}
	if p.Items == nil {
		p.Items = []Item{}
	}
	if len(p.Items) == 0 && next == "" {
		if q == "" {
			return StoreResult{Error: "Nothing here right now.", Page: p}
		}
		return StoreResult{Error: "Nothing found for \u201c" + q + "\u201d.", Page: p}
	}
	return StoreResult{Page: p}
}

// StoreItemView is an opened item: detail, choices, the default and the stream to play.
type StoreItemView struct {
	Detail   Detail   `json:"detail"`
	Choices  []Choice `json:"choices"`
	Default  string   `json:"default"`
	Stream   string   `json:"stream"`    // proxied path ("" = can't play right away)
	StreamMs int64    `json:"stream_ms"` // length
	Cost     string   `json:"cost"`      // "≈ 245 MB to watch"
	Error    string   `json:"error"`
	Note     string   `json:"note"`
}

func (m *Media) itemSource(it Item) Source {
	if it.Source == "link" && strings.HasPrefix(it.ID, "http") {
		return m.link
	}
	if s := m.Source(it.Source); s != nil {
		return s
	}
	return m.yt
}

// StoreOpen loads an item's qualities.
func (m *Media) StoreOpen(kind string, it Item) StoreItemView {
	s := m.itemSource(it)
	d, err := s.Detail(it, kind)
	if err != nil {
		return StoreItemView{Error: err.Error(), Choices: []Choice{}}
	}
	ch := s.Choices(kind, d)
	if ch == nil {
		ch = []Choice{}
	}
	v := StoreItemView{Detail: d, Choices: ch, StreamMs: int64(d.LengthSec) * 1000}
	def := s.DefaultChoice(kind, ch)
	if m.Prefs().DataSaver {
		if c := SaverChoice(kind, ch); c != nil {
			def = c
		}
	}
	if def != nil {
		v.Default = def.ID
	}
	var play []Format
	if strings.HasPrefix(d.ID, "yt:") {
		play = YtStreamPick(kind, d)
	} else if def != nil && len(def.Keys) == 1 {
		play = formatsFor(d, def.Keys)
	}
	if len(play) == 1 {
		v.Stream = m.AddStream(play[0].URL, s.UserAgent(), play[0].Mime)
		v.Cost = CostLine(streamBytes(play[0], d.LengthSec), kind)
	}
	if kind == "video" && !m.CanMerge() && strings.HasPrefix(d.ID, "yt:") {
		v.Note = "Install ffmpeg for 720p and 1080p downloads; without it YouTube offers its one-file qualities."
	}
	return v
}

func streamBytes(f Format, lengthSec int) int64 {
	if f.ContentLength > 0 {
		return f.ContentLength
	}
	return int64(f.Bitrate) * int64(lengthSec) / 8
}

// SaverChoice (data saver): music = smallest; video = smallest picture of at least 240p.
func SaverChoice(kind string, ch []Choice) *Choice {
	var best *Choice
	if kind == "video" {
		for i := range ch {
			if ch[i].Height >= 240 && (best == nil || ch[i].Height < best.Height) {
				best = &ch[i]
			}
		}
		if best == nil && len(ch) > 0 {
			best = &ch[len(ch)-1]
		}
		return best
	}
	for i := range ch {
		if ch[i].Bytes > 0 && (best == nil || ch[i].Bytes < best.Bytes) {
			best = &ch[i]
		}
	}
	return best
}

// StoreDownload queues one choice of an item.
func (m *Media) StoreDownload(kind string, it Item, choiceID string) (DlJob, error) {
	s := m.itemSource(it)
	d, err := s.Detail(it, kind)
	if err != nil {
		return DlJob{}, err
	}
	for _, c := range s.Choices(kind, d) {
		if c.ID == choiceID {
			return m.AddDownloadJob(it, d, c, kind), nil
		}
	}
	return DlJob{}, serr("That quality isn't offered any more. Open the item again.")
}
