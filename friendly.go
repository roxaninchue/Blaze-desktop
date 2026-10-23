package main

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"blaze-desktop/internal/runevents"
)

// plainRe: a model-written step summary that reads as words, not a command.
var plainRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9 ,.'()]*$`)

var installRe = regexp.MustCompile(`(?i)\b(npm|pnpm|yarn|bun) (i|install|add)\b|\bpip3? install\b|\b(apt|apt-get|dnf|yum|pacman|brew|winget|choco|scoop|cargo|go) (install|-S|get)\b`)

// friendlyTool says what a tool call is doing in plain English (the tray status line;
// app.js has the same phrases for the chat's status line).
func friendlyTool(name, rawArgs string) string {
	var a map[string]any
	_ = json.Unmarshal([]byte(rawArgs), &a)
	v := func(k string) string {
		s, _ := a[k].(string)
		return strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	}
	// first non-empty of keys, clipped for a pill line
	vs := func(n int, keys ...string) string {
		for _, k := range keys {
			if x := v(k); x != "" {
				if r := []rune(x); len(r) > n {
					x = strings.TrimSpace(string(r[:n-1])) + "…"
				}
				return x
			}
		}
		return ""
	}
	with := func(prefix, obj, alone string) string {
		if obj == "" {
			return alone
		}
		return prefix + obj
	}
	file := func() string {
		p := v("path")
		if p == "" {
			return "a file"
		}
		return filepath.Base(filepath.FromSlash(p))
	}
	var s string
	switch name {
	case "read_screen":
		s = "Reading your screen"
	case "see_screen":
		s = "Looking at your screen"
	case "notify":
		s = "Sending a notification"
	case "file_read":
		s = "Reading " + file()
	case "file_edit":
		s = "Editing " + file()
	case "file_write":
		s = "Writing " + file()
	case "file_delete":
		s = "Deleting " + file()
	case "file_list":
		s = "Looking through your files"
	case "file_search":
		s = `Searching your files for "` + orStr(v("pattern"), v("query")) + `"`
	case "web_search":
		s = `Searching the web for "` + v("query") + `"`
	case "fetch_page":
		s = "Reading " + orStr(hostOf(v("url")), "a web page")
	case "run_shell":
		s = "Running a command"
		if installRe.MatchString(v("command")) {
			s = "Installing packages"
		} else if sum := v("summary"); plainRe.MatchString(sum) && utf8.RuneCountInString(sum) <= 60 {
			s = strings.ToUpper(sum[:1]) + sum[1:]
		}
	case "git":
		s = "Checking the project history"
	case "run_code":
		s = "Running " + orStr(v("language"), "some") + " code"
	case "install_tool":
		s = "Installing " + orStr(v("name"), "a tool")
	case "update_blaze":
		s = "Checking for a Blaze update"
	case "remember":
		s = "Saving that to memory"
	case "schedule":
		s = "Setting up a schedule"
	case "use_skill", "load_skill":
		s = "Following a saved skill"
	case "recall":
		s = "Checking what I remember"
	case "find_tools":
		s = "Getting the right tools"
	case "system_info":
		s = "Checking your computer"
	case "forget":
		s = "Updating memory"
	case "write_skill":
		s = "Saving a skill"
	case "http_request":
		s = "Calling " + orStr(hostOf(v("url")), "a web service")
	case "download_file":
		s = "Downloading " + orStr(hostOf(v("url")), "a file")
	case "open_url":
		s = "Opening " + orStr(hostOf(orStr(v("target"), v("url"))), "a page")
	case "job_log", "job_input":
		s = "Checking a background job"
	case "job_stop":
		s = "Stopping a background job"
	case "csv_report":
		switch g, val := vs(20, "group_by"), vs(20, "value"); {
		case g != "" && val != "":
			s = "Charting " + val + " by " + g
		case g != "":
			s = "Charting " + orStr(baseOf(v("path")), "your CSV") + " by " + g
		default:
			s = with("Making a report from ", baseOf(v("path")), "Making your CSV report")
		}
	case "csv_summary":
		s = "Writing your summary"
	case "memory_save", "memory_update":
		s = with("Remembering ", vs(40, "subject"), "Saving that to memory")
	case "memory_search":
		s = with("Checking what I remember about ", vs(30, "query"), "Checking what I remember")
	case "schedule_add", "reminder_add", "set_reminder":
		switch t, at := vs(36, "text", "title", "what", "message", "task"), vs(16, "at", "when", "time"); {
		case t != "":
			s = "Setting a reminder to " + strings.TrimPrefix(t, "to ")
		case at != "":
			s = "Setting a reminder for " + at
		default:
			s = "Setting up a reminder"
		}
	case "get_weather":
		s = with("Checking the weather in ", vs(30, "place", "location", "city"), "Checking the weather")
	case "search_chat":
		s = with("Looking back through this chat for ", vs(24, "query"), "Looking back through this chat")
	case "read_turn", "read_artifact":
		s = "Rereading an earlier result"
	case "open_view":
		s = with("Opening ", vs(20, "view"), "Opening a view")
	case "update_brief":
		s = "Updating the project brief"
	case "music_play":
		s = with("Playing ", vs(36, "query", "song", "title", "artist"), "Playing music")
	case "media_download":
		s = "Downloading your media"
	case "dispatch_specialist":
		s = runevents.HelpLine(false, orStr(v("brief"), v("task")), "", v("name"))
	case "message_specialist":
		s = runevents.HelpLine(false, v("message"), "", v("name"))
	case "check_specialist":
		s = "Checking on the work in progress"
	case "cancel_specialist":
		s = "Stopping part of the work"
	case "apply_specialist":
		s = "Bringing in the reviewed changes"
		if v("action") == "discard" {
			s = "Setting aside the reviewed changes"
		}
	default:
		// an unknown tool still names its real object when the call has one
		switch {
		case v("summary") != "" && plainRe.MatchString(v("summary")):
			s = strings.ToUpper(v("summary")[:1]) + v("summary")[1:]
		case v("path") != "":
			s = "Working on " + baseOf(v("path"))
		case v("query") != "":
			s = `Looking up "` + vs(36, "query") + `"`
		case v("url") != "":
			s = "Working with " + hostOf(v("url"))
		default:
			s = "Working on it"
		}
	}
	if utf8.RuneCountInString(s) > 60 {
		r := []rune(s)
		s = string(r[:59]) + "…"
	}
	return s
}

func baseOf(p string) string {
	if p == "" {
		return ""
	}
	return filepath.Base(filepath.FromSlash(p))
}

func hostOf(s string) string {
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		return strings.TrimPrefix(u.Host, "www.")
	}
	return s
}
