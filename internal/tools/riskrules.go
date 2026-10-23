package tools

// Risk rules for commands that run someone else's code, send secrets out, publish, or
// change the user's shell start-up. None of them is destructive, so the danger patterns
// miss them, but a page read by fetch_page or web_search can steer a model into any of
// them. In Auto they ask first; Autopilot still approves them (they are not blocked).
//
// Self-contained on purpose (only regexp and strings) so Blaze Desktop can copy this file
// as-is: riskyExtra(cmd) is the whole API. Like the rest of guard.go this is pattern
// matching on the command text, not a sandbox.

import (
	"regexp"
	"strings"
)

const (
	// netFetch is a command that downloads something.
	netFetch = `(curl|wget|fetch|aria2c|iwr|irm|invoke-webrequest|invoke-restmethod|httpie|http|xh)`
	// interp is a program that runs code it reads from stdin or an argument.
	shells = `(sh|bash|zsh|dash|ksh|fish|ash|busybox\s+sh|pwsh|powershell|iex|invoke-expression|source|\.)`
	// interp runs a script from stdin only with no script argument (python3 -, node).
	interp = `(python[0-9.]*|perl|ruby|node|deno|bun|php)`
	// netSend is a command that can send data off the machine.
	netSend = `(curl|wget|nc|ncat|netcat|socat|telnet|scp|sftp|rsync|ftp|tftp|ssh|iwr|irm|invoke-webrequest|invoke-restmethod|send-mailmessage|mail|sendmail|http|xh|openssl\s+s_client)`
)

var (
	// pipeToInterp: anything piped into a shell or interpreter (curl … | sh,
	// base64 -d | bash, echo … | python). The source is usually a download or a decoder;
	// both are how remote code gets run, so any pipe into an interpreter asks.
	pipePre      = `(?i)\|\s*(sudo\s+(-\S+\s+)*)?(env\s+(\S+=\S*\s+)*)?(/\S*/)?`
	pipeToInterp = regexp.MustCompile(pipePre + shells + `(\s|$|;|&|\))|` + pipePre + interp + `(\s+-[a-z]?)*\s*($|;|&|\||\))`)

	// netSubst: a download fed to a shell by process or command substitution:
	// bash <(curl …), sh -c "$(curl …)", source <(wget -O- …), eval "$(curl …)", iex (irm …).
	netSubst = regexp.MustCompile(`(?i)(<\(\s*|\$\(\s*|` + "`" + `\s*|\b(iex|invoke-expression)\s*\(?\s*)` + netFetch + `\b`)

	// decodeExec: a decoded blob run directly (eval "$(echo … | base64 -d)", python -c
	// "exec(base64.b64decode(…))", powershell -EncodedCommand).
	decodeExec = regexp.MustCompile(`(?i)(\beval\b[^;\n]*\b(base64|xxd|openssl)\b|\bexec\s*\(\s*(base64\.)?b64decode|\b(pwsh|powershell)(\.exe)?\b[^;\n]*\s-(e|enc|encodedcommand)\s)`)

	// credPath names files that hold keys, tokens and passwords.
	credPath = regexp.MustCompile(`(?i)(~/\.ssh\b|/\.ssh\b|\bid_(rsa|dsa|ecdsa|ed25519)\b|\.pem\b|\.p12\b|\.pfx\b|(^|[\s/=@<])\.env(\.[\w-]+)?\b|\.aws/credentials|\.aws/config|\.netrc\b|\.npmrc\b|\.pypirc\b|\.git-credentials\b|\.docker/config\.json|\.kube/config|\.gnupg/|\.config/gh/hosts\.yml|\.config/gcloud/|\.azure/|\bcredentials\.json\b|\btokens?\.(json|txt)\b|\.vault-token\b|/etc/shadow\b|keychain|\bsecring\b)`)

	netSendRe = regexp.MustCompile(`(?i)(^|[\s;&|(` + "`" + `$])` + netSend + `(\s|$)`)

	// keyRead: reading private key material outright (it would land in the chat sent to
	// the model provider).
	keyRead = regexp.MustCompile(`(?i)\b(cat|type|less|more|head|tail|base64|xxd|od|strings|get-content|gc|cp|copy)\b[^;|&\n]*(\bid_(rsa|dsa|ecdsa|ed25519)(\s|$|[;|&>)])|\.aws/credentials|\.git-credentials|\.netrc\b|/etc/shadow\b|\.gnupg/)`)

	// publish: pushing code or packages somewhere other people get them.
	publish = regexp.MustCompile(`(?i)(\bgit\b(\s+(-C|-c|--git-dir|--work-tree)(\s+|=)\S+|\s+--?[\w-]+)*\s+push\b|\b(npm|yarn|pnpm|bun)\s+(publish|release)\b|\bcargo\s+(publish|yank)\b|\btwine\s+upload\b|\bpoetry\s+publish\b|\bflit\s+publish\b|\buv\s+publish\b|\bgem\s+(push|yank)\b|\bdotnet\s+nuget\s+push\b|\bnuget\s+push\b|\b(docker|podman)\s+(image\s+)?push\b|\bhelm\s+push\b|\bgh\s+(release\s+create|repo\s+create)\b|\bglab\s+release\s+create\b|\bvsce\s+publish\b|\bmvn\b[^;&|\n]*\bdeploy\b|\bgradlew?\b[^;&|\n]*\bpublish)`)

	// dotWrite: writing into a file under ~ whose name starts with a dot (shell start-up,
	// ssh, git config), or replacing the crontab. Redirects, tee, sed -i, cp/mv/ln.
	dotTarget = `~/\.[\w.-]+`
	dotWrite  = regexp.MustCompile(`(?i)((>>?|>\|)\s*` + dotTarget + `|\btee\b(\s+-\S+)*\s+` + dotTarget + `|\bsed\b[^;|&\n]*\s-i\S*\b[^;|&\n]*\s` + dotTarget + `|\b(cp|mv|ln|install|rsync)\b[^;|&\n]*\s` + dotTarget + `\s*($|[;|&])|\b(add-content|set-content|out-file)\b[^;|&\n]*(\$profile|` + dotTarget + `)|\bcrontab\s+(-[^l\s]\S*\s+)*[^-\s]|\bcrontab\s+-\s*$|\bcrontab\s+-r\b)`)
)

// riskyExtra is true when cmd runs fetched or decoded code, sends credential files off
// the machine, reads private keys, publishes or pushes, or writes a dotfile in ~.
func riskyExtra(cmd string) bool {
	for _, s := range []string{cmd, normalizeExtra(cmd)} {
		if pipeToInterp.MatchString(s) || netSubst.MatchString(s) || decodeExec.MatchString(s) ||
			publish.MatchString(s) || dotWrite.MatchString(s) || keyRead.MatchString(s) {
			return true
		}
		if credPath.MatchString(s) && netSendRe.MatchString(s) {
			return true
		}
	}
	return false
}

var homeVarsExtra = regexp.MustCompile(`(?i)\$\{HOME\}|\$HOME\b|\$\{env:(USERPROFILE|HOME)\}|\$env:(USERPROFILE|HOME)\b|%USERPROFILE%|%HOMEDRIVE%%HOMEPATH%`)

// normalizeExtra drops quotes and backslash escapes and writes $HOME spellings as ~.
func normalizeExtra(cmd string) string {
	s := strings.NewReplacer(`"`, "", `'`, "", `\`+"\n", " ").Replace(cmd)
	s = homeVarsExtra.ReplaceAllString(s, "~")
	return s
}
