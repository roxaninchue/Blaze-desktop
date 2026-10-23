package runevents

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"blaze-desktop/internal/privacy"
)

// Max is the longest pill line (runes).
const Max = 60

var (
	jsonish  = regexp.MustCompile(`[{\[][\s\S]*$`)             // a raw tool call's arguments / JSON, to the end
	toolName = regexp.MustCompile(`\b[a-z]+(?:_[a-z0-9]+)+\b`) // snake_case tool names (run_shell, file_write)
	callish  = regexp.MustCompile(`\w+\([^)]*\)`)              // name(args)
	ws       = regexp.MustCompile(`\s+`)
)

// Sanitize makes a step line safe for the pill: no provider, model or owner names, no raw tool
// JSON, arguments or tool ids, one line, at most Max runes. "" when nothing readable is left.
func Sanitize(s string) string {
	s = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	s = jsonish.ReplaceAllString(s, "")
	s = callish.ReplaceAllString(s, "")
	s = dropToolNames(s)
	s = strings.NewReplacer("`", "", "\"", "", "<", "", ">", "").Replace(s)
	if privacy.Leaks(s) || privacy.LeaksOwner(s) {
		return "" // a line about the model or the maker says nothing the pill should show
	}
	s = strings.Trim(ws.ReplaceAllString(s, " "), " :;,-·|")
	if s == "" || !regexp.MustCompile(`[A-Za-z]{2}`).MatchString(s) {
		return ""
	}
	if utf8.RuneCountInString(s) > Max {
		r := []rune(s)
		s = strings.TrimSpace(string(r[:Max-1])) + "…"
	}
	return s
}

// dropToolNames removes snake_case tool names but keeps a file name that has one
// ("cafe_sales.csv" stays: the pill names the real object).
func dropToolNames(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range toolName.FindAllStringIndex(s, -1) {
		if m[1]+1 < len(s) && s[m[1]] == '.' && isWordByte(s[m[1]+1]) {
			continue
		}
		b.WriteString(s[last:m[0]])
		last = m[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

func isWordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}
