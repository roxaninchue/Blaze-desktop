package tools

import (
	"regexp"
	"runtime"
	"strings"
)

func eqPath(a, b string) bool {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// v0.19.13: commands a web page could steer the model into ask first in Auto
// (Bug Diver review 2026-10-22, Critical #3). riskyExtra (riskrules.go) is the rule
// set shared with the CLI; deskExtra adds Windows/autostart persistence and reading
// the whole environment or a keystore while sending data out.
var (
	deskPersist = regexp.MustCompile(`(?i)(\bschtasks\s+/create\b|\bsetx\b|\breg\s+add\b[^\n]*\\run\b|\bsystemctl\s+(--user\s+)?enable\b|\blaunchctl\s+(load|bootstrap)\b|\.config[/\\]autostart|microsoft\.powershell_profile\.ps1|>>?\s*\$profile\b)`)
	deskSecret  = regexp.MustCompile(`(?i)(\b(print` + `env|env)\s*(\||>|$)|\bsecurity\s+(find|dump)-(generic|internet)-password|\bcmdkey\s+/list|\bvaultcmd\b)`)
	deskSend    = regexp.MustCompile(`(?i)(\bcurl\b|\bwget\b|\bnc\b|\bncat\b|\bsocat\b|\bscp\b|invoke-webrequest|invoke-restmethod|\biwr\b|\birm\b|/dev/tcp/)`)

	// deskCred: secret stores the shared credPath list doesn't name: Blaze's own keys and
	// account tokens (~/.blaze, ~/.config/blaze), browser profiles (saved passwords,
	// cookies), OS keychains and password managers. Sent anywhere: ask.
	deskCred = regexp.MustCompile(`(?i)(\.blaze[/\\](keys|account|keystore|tokens?|auth|session)|\.config[/\\]blaze\b|login data|\bcookies\.sqlite\b|[/\\](default|network)[/\\]cookies\b|\bkey[34]\.db\b|\blogins\.json\b|library[/\\]keychains|\.local[/\\]share[/\\]keyrings|\.password-store|\.kdbx\b|\.mozilla[/\\]firefox|\.config[/\\](google-chrome|chromium|bravesoftware|microsoft-edge|bitwarden|1password)|appdata[/\\](local|roaming)[/\\](google[/\\]chrome|microsoft[/\\]edge|bravesoftware|mozilla[/\\]firefox|bitwarden|1password)|library[/\\]application support[/\\](google[/\\]chrome|bravesoftware|microsoft edge|firefox|bitwarden|1password))`)
	// deskCredRead: copying or dumping one of them outright (it lands in the chat sent to
	// the model provider), or opening a browser's password/cookie database.
	deskCredRead = regexp.MustCompile(`(?i)(\b(cat|type|less|more|head|tail|base64|xxd|od|strings|get-content|gc|cp|copy|tar|zip|7z|sqlite3)\b[^;|&\n]*(\.blaze[/\\](keys|account|keystore|tokens?|auth|session)|\.config[/\\]blaze[/\\]\S*(key|token|account|auth)|login data|\bcookies\.sqlite\b|[/\\](default|network)[/\\]cookies\b|\bkey[34]\.db\b|\blogins\.json\b|library[/\\]keychains|\.local[/\\]share[/\\]keyrings|\.password-store|\.kdbx\b))`)
)

// riskyNet is the v0.19.13 addition to risky().
func riskyNet(cmd string) bool {
	if riskyExtra(cmd) || psDownloadExec.MatchString(cmd) || deskPersist.MatchString(cmd) || (deskSecret.MatchString(cmd) && deskSend.MatchString(cmd)) {
		return true
	}
	for _, s := range []string{cmd, normalizeExtra(cmd)} {
		if deskCredRead.MatchString(s) || (deskCred.MatchString(s) && (deskSend.MatchString(s) || netSendRe.MatchString(s))) {
			return true
		}
	}
	return false
}

// psDownloadExec catches PowerShell running downloaded code: iex (New-Object Net.WebClient).DownloadString(...), Invoke-Expression with Net.Http/DownloadData.
var psDownloadExec = regexp.MustCompile(`(?i)\b(iex|invoke-expression)\b.*(net\.webclient|downloadstring|downloaddata|downloadfile|net\.http\.httpclient|start-bitstransfer)|(net\.webclient|downloadstring)[^|;]*\|\s*(iex|invoke-expression)\b`)

// shellRefusal is the tool result for a blocked command.
func shellRefusal(why string) string {
	return "Refused: " + why + ". Blaze never runs this, in any mode. Use a narrower command on the exact path you mean, or ask the user to do it themselves."
}
