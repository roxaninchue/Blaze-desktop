package tools

// web_search backends (Desktop v0.19.13, same as Blaze CLI v0.14.15). DuckDuckGo's HTML page
// was the only source; it often answers with a page that has no results (rate limiting,
// bot checks), so web_search said "No results." and the model gave up or apologised. Now: DuckDuckGo with a short
// timeout, then Bing's RSS, then DuckDuckGo Lite; a news-ish query ("latest", "news",
// "today"…) also gets Google News RSS first, with dates. The first source with results
// wins, and a total failure says plainly to answer from fetch_page on a known site.

import (
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type hit struct{ Title, Link, Snippet, Date string }

var newsWords = regexp.MustCompile(`(?i)\b(news|latest|today|todays|today's|this week|headlines?|breaking|current|recent|announce\w*|released?|update[sd]?)\b`)

var (
	reDDGLite = regexp.MustCompile(`(?is)<a[^>]+href="([^"]+)"[^>]+class=['"]result-link['"][^>]*>(.*?)</a>.*?<td[^>]+class=['"]result-snippet['"][^>]*>(.*?)</td>`)
	searchTO  = 8 * time.Second
)

// searchGet is get with a short per-source timeout, so one dead source costs seconds.
var searchGet = func(parent context.Context, u string) (int, string, error) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, searchTO)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept-Language", "en-US,en;q=0.8")
	resp, err := httpc.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, string(b), nil
}

type rssDoc struct {
	Items []struct {
		Title   string `xml:"title"`
		Link    string `xml:"link"`
		Desc    string `xml:"description"`
		PubDate string `xml:"pubDate"`
		Source  string `xml:"source"`
	} `xml:"channel>item"`
}

func parseRSS(body string, n int) []hit {
	var d rssDoc
	if xml.Unmarshal([]byte(body), &d) != nil {
		return nil
	}
	var out []hit
	for _, it := range d.Items {
		if len(out) >= n {
			break
		}
		sn := htmlText(it.Desc)
		if it.Source != "" && !strings.Contains(sn, it.Source) {
			sn = strings.TrimSpace(it.Source + ". " + sn)
		}
		if len(sn) > 300 {
			sn = sn[:300] + "…"
		}
		date := it.PubDate
		if t, err := time.Parse(time.RFC1123, date); err == nil {
			date = t.Format("2 Jan 2006")
		} else if t, err := time.Parse(time.RFC1123Z, date); err == nil {
			date = t.Format("2 Jan 2006")
		}
		out = append(out, hit{Title: strings.TrimSpace(html.UnescapeString(it.Title)), Link: strings.TrimSpace(it.Link), Snippet: sn, Date: date})
	}
	return out
}

func parseDDG(body string, n int) []hit {
	var out []hit
	for _, m := range reDDG.FindAllStringSubmatch(body, n) {
		out = append(out, hit{Title: htmlText(m[2]), Link: ddgLink(m[1]), Snippet: htmlText(m[3])})
	}
	return out
}

func parseDDGLite(body string, n int) []hit {
	var out []hit
	for _, m := range reDDGLite.FindAllStringSubmatch(body, n) {
		out = append(out, hit{Title: htmlText(m[2]), Link: ddgLink(m[1]), Snippet: htmlText(m[3])})
	}
	return out
}

func ddgLink(l string) string {
	l = html.UnescapeString(l)
	if strings.HasPrefix(l, "//") {
		l = "https:" + l
	}
	if u, err := url.Parse(l); err == nil && u.Query().Get("uddg") != "" {
		return u.Query().Get("uddg")
	}
	return l
}

type source struct {
	name  string
	url   func(q string) string
	parse func(body string, n int) []hit
}

var (
	srcGoogleNews = source{"Google News", func(q string) string {
		return "https://news.google.com/rss/search?hl=en-US&gl=US&ceid=US:en&q=" + url.QueryEscape(q)
	}, parseRSS}
	srcDDG = source{"DuckDuckGo", func(q string) string {
		return "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(q)
	}, parseDDG}
	srcBing = source{"Bing", func(q string) string {
		return "https://www.bing.com/search?format=rss&q=" + url.QueryEscape(q)
	}, parseRSS}
	srcDDGLite = source{"DuckDuckGo Lite", func(q string) string {
		return "https://lite.duckduckgo.com/lite/?q=" + url.QueryEscape(q)
	}, parseDDGLite}
)

func searchSources(q string) []source {
	s := []source{srcDDG, srcBing, srcDDGLite}
	if newsWords.MatchString(q) {
		s = append([]source{srcGoogleNews}, s...)
	}
	return s
}

// webSearch tries each source in turn and returns the first non-empty result list.
func webSearch(ctx context.Context, q string, n int) string {
	if n <= 0 {
		n = 8
	}
	var fails []string
	for _, s := range searchSources(q) {
		if ctx != nil && ctx.Err() != nil {
			return "Error: search stopped."
		}
		code, body, err := searchGet(ctx, s.url(q))
		switch {
		case err != nil:
			fails = append(fails, s.name+": "+shortErr(err))
			continue
		case code >= 300:
			fails = append(fails, fmt.Sprintf("%s: HTTP %d", s.name, code))
			continue
		}
		hs := s.parse(body, n)
		if len(hs) == 0 {
			fails = append(fails, s.name+": no results")
			continue
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Results from %s for %q:\n", s.name, q)
		for i, h := range hs {
			fmt.Fprintf(&b, "%d. %s\n   %s\n", i+1, h.Title, h.Link)
			if h.Date != "" {
				fmt.Fprintf(&b, "   %s", h.Date)
				if h.Snippet != "" {
					b.WriteString(" · ")
				} else {
					b.WriteString("\n")
				}
			} else if h.Snippet != "" {
				b.WriteString("   ")
			}
			if h.Snippet != "" {
				b.WriteString(h.Snippet + "\n")
			}
		}
		b.WriteString("These snippets are often enough to answer; fetch_page only the one or two links you need. In your answer give each item its source and link.")
		return b.String()
	}
	return "Error: web search is unreachable from this machine (" + strings.Join(fails, "; ") + "). Don't retry search. " +
		"Read one or two well-known pages with fetch_page instead (for news: https://news.google.com/rss/search?q=" + url.QueryEscape(q) +
		" or a site's RSS feed), then answer, or tell the user search is blocked here."
}

func shortErr(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 && i+2 < len(s) {
		s = s[i+2:]
	}
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}
