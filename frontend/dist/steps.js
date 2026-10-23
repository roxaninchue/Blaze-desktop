// Plain words for what the agent is doing (v0.4.6).
// STEPS.describe(name, args) turns a tool call into a short phrase ("Reading package.json",
// "Running the tests") with no commands, paths or JSON. It is a small deterministic table:
// no model calls. STEPS.detail(name, args) is the exact command or tool call, shown only
// inside an expanded step. STEPS.plainStatus(line) cleans the backend's progress lines.
// Runs in the browser (window.STEPS) and under node (module.exports) for ci/steps.test.js.
(function (root) {
  "use strict";
  const MAX = 60;
  const clip = (t, n = MAX) => { t = String(t || "").replace(/\s+/g, " ").trim(); return t.length > n ? t.slice(0, n - 1) + "…" : t; };
  const unq = s => String(s || "").replace(/^(['"])([\s\S]*)\1$/, "$2");
  const isFlag = s => /^-/.test(s) || /^\/[A-Za-z?]$/.test(s);
  // A path as a person would say it: the last part, or "your home folder" / "the project folder".
  function place(p, kind) {
    p = unq(String(p || "").trim()).replace(/[\\/]+$/, "");
    if (!p || p === "." || p === "./") return kind === "folder" ? "the project folder" : "";
    if (p === "~" || p === "$HOME" || p === "%USERPROFILE%") return "your home folder";
    if (p === "..") return "the parent folder";
    if (p === "/") return "the whole disk";
    if (/[*?]/.test(p)) {
      const ext = (p.match(/\*\.([A-Za-z0-9]+)$/) || [])[1];
      return ext ? `the .${ext} files` : "matching files";
    }
    const b = p.split(/[\\/]/).filter(Boolean).pop() || p;
    return b.length > 40 ? b.slice(0, 39) + "…" : b;
  }
  const host = u => { try { return new URL(/^[a-z]+:\/\//i.test(u) ? u : "https://" + u).host.replace(/^www\./, ""); } catch (e) { return ""; } };
  const words = s => String(s || "").replace(/[_-]+/g, " ").trim();
  const cap = s => s ? s[0].toUpperCase() + s.slice(1) : s;

  // ---------- shell ----------
  // Split a command line into words, keeping quoted strings together.
  function tokens(s) {
    const out = []; let cur = "", q = "", has = false;
    for (let i = 0; i < s.length; i++) {
      const c = s[i];
      if (q) { if (c === q) q = ""; else cur += c; continue; }
      if (c === "'" || c === '"') { q = c; has = true; continue; }
      if (/\s/.test(c)) { if (cur || has) out.push(cur); cur = ""; has = false; continue; }
      cur += c;
    }
    if (cur || has) out.push(cur);
    return out;
  }
  // Split on ; && || | and newlines (outside quotes). Each part knows if it came after a pipe.
  function segments(cmd) {
    const out = []; let cur = "", q = "", piped = false;
    const push = next => { if (cur.trim()) out.push({ text: cur.trim(), piped }); cur = ""; piped = next; };
    for (let i = 0; i < cmd.length; i++) {
      const c = cmd[i], n = cmd[i + 1];
      if (q) { cur += c; if (c === q) q = ""; continue; }
      if (c === "'" || c === '"') { q = c; cur += c; continue; }
      if ((c === "&" && n === "&") || (c === "|" && n === "|")) { push(false); i++; continue; }
      if (c === "|") { push(true); continue; }
      if (c === ";" || c === "\n") { push(false); continue; }
      if (c === "&" && n !== ">" && cmd[i - 1] !== ">") { push(false); continue; }
      cur += c;
    }
    push(false);
    return out;
  }
  const INSTALL_SUB = { npm: /^(i|install|ci|add)$/, pnpm: /^(i|install|add)$/, yarn: /^(install|add)?$/, bun: /^(i|install|add)$/, pip: /^install$/, pip3: /^install$/, uv: /^(add|sync|pip)$/, poetry: /^(install|add)$/, cargo: /^(add|fetch)$/, go: /^(get|mod)$/, composer: /^(install|require)$/, bundle: /^(install)?$/, gem: /^install$/, pipenv: /^install$/, conda: /^install$/, deno: /^(add|install)$/ };
  const SYS_PKG = { apt: /^install$/, "apt-get": /^install$/, dnf: /^install$/, yum: /^install$/, pacman: /^-S/, yay: /^-S/, paru: /^-S/, brew: /^install$/, winget: /^install$/, choco: /^install$/, scoop: /^install$/, snap: /^install$/, flatpak: /^install$/, zypper: /^(in|install)$/, apk: /^add$/ };
  const TESTERS = /^(pytest|jest|vitest|mocha|rspec|phpunit|tox|nox|ava|karma|playwright|cypress)$/;
  const READERS = /^(cat|less|more|head|tail|bat|batcat|nl|type|get-content|gc|xxd|hexdump|strings|od)$/;
  const LISTERS = /^(ls|ll|la|dir|tree|exa|eza|get-childitem|gci)$/;
  const FINDERS = /^(find|fd|fdfind|locate)$/;
  const SEARCHERS = /^(grep|egrep|fgrep|rg|ag|ack|findstr|select-string|sls|git-grep)$/;
  const FILTERS = /^(grep|egrep|rg|head|tail|sort|uniq|wc|awk|sed|cut|tr|jq|less|more|column|xargs|tee|cat|findstr|select-string|sls|nl|fold|fmt)$/;
  const QUIET = /^(echo|printf|true|false|:|sleep|clear|cls|pwd|whoami|set|export|source|\.|exit|read|wait|date|unset|alias|write-host|write-output)$/;

  // One part of a command → { p: priority, t: phrase, k: kind, x: target }.
  function part(seg) {
    let text = seg.text;
    // Output redirected into a file: "echo x > notes.md" is writing that file.
    let redirect = "";
    const rm = text.match(/(?:^|[^0-9&>])>>?\s*("[^"]+"|'[^']+'|[^\s;&|<>]+)/);
    if (rm && !/^\/dev\/null$/.test(unq(rm[1])) && !/^&\d$/.test(rm[1])) redirect = unq(rm[1]);
    text = text.replace(/\d?>>?\s*&?\s*("[^"]+"|'[^']+'|[^\s;&|<>]+)/g, " ").replace(/<<-?\s*['"]?\w+['"]?/g, " ").replace(/<\s*\S+/g, " ");
    let w = tokens(text);
    // Drop wrappers and VAR=value prefixes.
    while (w.length && (/^[A-Za-z_][A-Za-z0-9_]*=/.test(w[0]) || /^(sudo|doas|time|nohup|env|command|exec|nice|caffeinate|stdbuf|timeout|npx|pnpx|bunx|uvx|call|start-process|&)$/i.test(w[0]))) {
      if (/^timeout$/i.test(w[0]) && w[1] && /^\d/.test(w[1])) w.shift();
      w.shift();
    }
    if (!w.length) return { p: 0, t: "", k: "quiet" };
    let c = w[0].split(/[\\/]/).pop().toLowerCase().replace(/\.(exe|cmd|bat|ps1)$/, "");
    const a = w.slice(1);
    const pos = a.filter(x => !isFlag(x));
    const sub = (pos[0] || "").toLowerCase();
    const R = (p, t, k, x) => ({ p, t, k: k || "run", x: x || "" });
    if (c === "python3" || c === "py") c = "python";
    if (c === "pip3") c = "pip";

    // Writing into a file wins over what produced the text.
    if (redirect && (QUIET.test(c) || c === "cat" || c === "jq" || c === "sed" || c === "awk")) return R(5, "Writing " + place(redirect), "write", place(redirect));
    if (QUIET.test(c)) return R(0, "", "quiet");
    if (c === "cd" || c === "pushd" || c === "popd" || c === "set-location") return R(0.5, "Opening " + place(pos[0] || "~", "folder"), "cd");

    // Installs.
    if (SYS_PKG[c] && a.some(x => SYS_PKG[c].test(x))) {
      const pk = pos.filter(x => !SYS_PKG[c].test(x) && !/^(install|-S\w*)$/.test(x));
      return R(9, pk.length === 1 ? "Installing " + place(pk[0]) : "Installing dependencies", "install");
    }
    if (c === "python" && a[0] === "-m" && /^pip$/.test(a[1] || "") && /install/.test(a[2] || "")) return R(9, "Installing dependencies", "install");
    if (INSTALL_SUB[c] && (INSTALL_SUB[c].test(sub) || (c === "yarn" && !pos.length))) {
      if (c === "go" && sub === "mod" && !/^(download|tidy)$/.test(pos[1] || "")) return R(3, "Checking Go modules");
      if (c === "go" && sub === "mod" && pos[1] === "tidy") return R(6, "Tidying Go modules");
      return R(9, "Installing dependencies", "install");
    }
    if ((c === "go" || c === "cargo") && sub === "install") return R(9, "Installing " + place((pos[1] || "a tool").replace(/@.*$/, "")), "install");
    if (c === "pipx" && sub === "install") return R(9, "Installing " + place(pos[1] || "a tool"), "install");

    // Tests, builds, linters, scripts.
    const pkgRun = /^(npm|pnpm|yarn|bun)$/.test(c);
    const script = pkgRun ? (sub === "run" || sub === "run-script" ? (pos[1] || "") : sub) : "";
    if (pkgRun && /^(t|test|tests|test:.*|e2e|spec)$/.test(script)) return R(8, "Running the tests", "test");
    if ((c === "go" || c === "cargo" || c === "dotnet" || c === "deno" || c === "bun" || c === "mix" || c === "swift") && sub === "test") return R(8, "Running the tests", "test");
    if (c === "python" && a[0] === "-m" && /^(pytest|unittest|tox|nose2)$/.test(a[1] || "")) return R(8, "Running the tests", "test");
    if (TESTERS.test(c) || (c === "make" && /^(test|check|tests)$/.test(sub)) || ((c === "gradle" || c === "gradlew" || c === "mvn") && /^(test|check)$/.test(sub))) return R(8, "Running the tests", "test");
    if (c === "docker" && (sub === "build" || (sub === "buildx" && pos[1] === "build"))) return R(7, "Building a Docker image", "build");
    if (pkgRun && /^(build|build:.*|compile|dist|bundle|package)$/.test(script)) return R(7, "Building the project", "build");
    if (((c === "go" || c === "cargo" || c === "dotnet" || c === "wails" || c === "swift" || c === "zig") && sub === "build") || c === "tsc" || c === "webpack" || c === "esbuild" || c === "rollup" || c === "cmake" || c === "ninja" || c === "msbuild" || c === "javac" || c === "gcc" || c === "g++" || c === "clang" || c === "rustc" ||
      (c === "make" && (!sub || /^(all|build|release|install)$/.test(sub))) || ((c === "vite" || c === "next" || c === "nuxt" || c === "astro") && sub === "build") || ((c === "gradle" || c === "gradlew" || c === "mvn") && /^(build|assemble|package|compile|install)$/.test(sub)))
      return R(7, "Building the project", "build");
    if (/^(eslint|ruff|flake8|pylint|golangci-lint|staticcheck|shellcheck|stylelint|mypy|biome)$/.test(c) || (c === "go" && sub === "vet") || (c === "cargo" && sub === "clippy") || (pkgRun && /^(lint|lint:.*|typecheck|check)$/.test(script))) return R(6, "Checking the code for problems", "lint");
    if (/^(prettier|gofmt|goimports|black|rustfmt|isort|clang-format)$/.test(c) || (c === "go" && sub === "fmt") || (c === "cargo" && sub === "fmt") || (pkgRun && /^(format|fmt)$/.test(script))) return R(6, "Formatting the code", "format");
    if ((pkgRun && /^(dev|start|serve|preview|watch)$/.test(script)) || ((c === "vite" || c === "next" || c === "nuxt" || c === "astro") && (!sub || /^(dev|start|preview)$/.test(sub))) || (c === "wails" && sub === "dev") || (c === "python" && a[0] === "-m" && a[1] === "http.server") || (c === "php" && a[0] === "-S") || (c === "rails" && /^(s|server)$/.test(sub)))
      return R(6, "Starting the dev server", "serve");
    if (pkgRun && script && sub === "run") return R(6, `Running the ${words(script.split(":")[0])} script`);
    if (pkgRun && /^(outdated|ls|list|audit|why)$/.test(sub)) return R(3, "Checking the dependencies");
    if (c === "make" && sub) return R(6, `Running the ${words(sub)} task`);
    if (c === "just" || c === "task") return R(6, sub ? `Running the ${words(sub)} task` : "Running a task");

    // Version / presence checks.
    if (a.length === 1 && /^(--version|-v|-V|version)$/.test(a[0])) return R(2, `Checking the ${c} version`, "check");
    if (/^(which|where|whereis|type|get-command|gcm)$/.test(c) && pos[0] && c !== "type") return R(2, `Checking ${place(pos[0])} is installed`, "check");

    // Running programs and scripts.
    if (c === "go" && sub === "run") return R(6, "Running " + (pos[1] && pos[1] !== "." ? place(pos[1]) : "the program"));
    if (c === "cargo" && sub === "run") return R(6, "Running the program");
    if (/^(python|node|deno|bun|ruby|perl|php|bash|sh|zsh|pwsh|powershell|lua|java|tsx|ts-node|osascript)$/.test(c)) {
      if (a.some(x => /^(-c|-e|--eval|-command|-Command|-p)$/.test(x)) || !pos.length || (c === "deno" && sub === "eval")) return R(4, "Running a quick script");
      const f = c === "deno" && sub === "run" ? pos[1] : pos[0];
      return R(6, "Running " + place(f));
    }
    if (/^\.{0,2}\//.test(w[0]) && !/\s/.test(w[0])) return R(6, "Running " + place(w[0]));

    // Git.
    if (c === "git" || c === "gh") {
      let g = a.slice(); while (g.length && (isFlag(g[0]) || /^(-C|-c)$/.test(g[0]))) { if (/^(-C|-c)$/.test(g[0])) g.shift(); g.shift(); }
      const s = (g[0] || "").toLowerCase();
      if (c === "gh") {
        if (s === "pr") return R(5, /^(create|merge)$/.test(g[1] || "") ? (g[1] === "merge" ? "Merging a pull request" : "Opening a pull request") : "Checking pull requests");
        if (s === "issue") return R(5, g[1] === "create" ? "Opening an issue" : "Checking issues");
        if (s === "run" || s === "workflow") return R(4, "Checking the CI runs");
        if (s === "repo" && g[1] === "clone") return R(7, "Cloning " + place(g[2] || "a repository"));
        return R(4, "Checking GitHub");
      }
      const G = { status: [3, "Checking git status"], diff: [3, "Checking what changed"], log: [3, "Checking git history"], show: [3, "Checking git history"], blame: [3, "Checking git history"], shortlog: [3, "Checking git history"], reflog: [3, "Checking git history"],
        add: [5, "Staging changes"], commit: [6, "Committing changes"], push: [6, "Pushing changes"], pull: [6, "Pulling the latest changes"], fetch: [5, "Fetching the latest changes"],
        checkout: [5, "Switching branches"], switch: [5, "Switching branches"], branch: [3, "Checking branches"], merge: [6, "Merging changes"], rebase: [6, "Rebasing changes"], stash: [5, "Stashing changes"],
        reset: [6, "Resetting changes"], restore: [6, "Restoring files"], tag: [4, "Tagging a release"], remote: [3, "Checking git remotes"], init: [6, "Setting up git"], grep: [4, "Searching the code"], "rev-parse": [2, "Checking git status"], "ls-files": [2, "Looking through tracked files"], config: [2, "Checking git settings"], "cherry-pick": [6, "Applying a commit"], rm: [6, "Removing files from git"], mv: [6, "Moving files in git"] };
      if (s === "clone") { const u = g.filter(x => !isFlag(x))[1] || ""; return R(7, "Cloning " + (place(u.replace(/\.git$/, "")) || "a repository")); }
      if (s === "grep") { const term = g.slice(1).filter(x => !isFlag(x))[0]; if (term) return R(4, `Searching the code for "${clip(term, 30)}"`, "search"); }
      if (s === "checkout" && g.includes("-b")) return R(5, "Creating a branch");
      if (s === "branch" && g.length > 1 && !g.slice(1).every(isFlag)) return R(5, "Creating a branch");
      return G[s] ? R(G[s][0], G[s][1], "git") : R(3, "Using git", "git");
    }

    // Reading, listing, searching.
    if (READERS.test(c)) {
      const f = pos.filter(x => !/^\d+$/.test(x));
      if (!f.length) return seg.piped ? R(0.2, "", "filter") : R(2, "Reading a file", "read");
      return R(seg.piped ? 0.2 : 3, "Reading " + place(f[0]), "read", place(f[0]));
    }
    if (LISTERS.test(c)) { const d = pos.filter(x => !/^\d+$/.test(x))[0]; return R(2, "Looking through " + place(d, "folder"), "list", place(d, "folder")); }
    if (FINDERS.test(c)) {
      const nm = (a[a.indexOf("-name") + 1] || a[a.indexOf("-iname") + 1] || "");
      if (c === "locate" && pos[0]) return R(3, `Looking for "${clip(pos[0], 30)}"`, "list");
      if ((a.includes("-name") || a.includes("-iname")) && nm) return R(3, `Looking for ${place(nm)} in ${place(pos[0], "folder")}`, "list");
      return R(2, "Looking through " + place(pos[0], "folder"), "list", place(pos[0], "folder"));
    }
    if (SEARCHERS.test(c)) {
      if (seg.piped) return R(0.2, "", "filter");
      const ix = a.findIndex(x => /^(-e|--regexp|-pattern)$/i.test(x));
      const term = ix >= 0 ? a[ix + 1] : pos[0];
      return term ? R(4, `Searching for "${clip(term, 30)}"`, "search", clip(term, 30)) : R(3, "Searching the files", "search");
    }
    if (seg.piped && FILTERS.test(c)) return R(0.2, "", "filter");
    if (c === "wc") return R(3, "Counting lines in " + (place(pos[0]) || "a file"), "read");
    if (c === "du") return R(3, "Checking folder sizes", "check");
    if (c === "df" || c === "get-psdrive") return R(3, "Checking disk space", "check");
    if (c === "free" || c === "vm_stat") return R(3, "Checking memory use", "check");
    if (/^(uname|lsb_release|systeminfo|neofetch|fastfetch|hostnamectl|sw_vers|ver|lscpu|lspci|lsusb|nvidia-smi|sysctl)$/.test(c)) return R(3, "Checking your computer", "check");
    if (/^(ps|top|htop|btop|pgrep|tasklist|get-process|lsof|ss|netstat)$/.test(c)) return R(3, /^(lsof|ss|netstat)$/.test(c) ? "Checking open ports" : "Checking running programs", "check");
    if (/^(kill|pkill|killall|taskkill|stop-process)$/.test(c)) return R(5, "Stopping a program");
    if (c === "jq" && pos[1]) return R(3, "Reading " + place(pos[1]), "read");
    if (c === "stat" || c === "file") return R(2, "Checking " + (place(pos[0]) || "a file"), "read");

    // Changing files.
    if ((c === "sed" && a.some(x => /^-i/.test(x))) || (c === "perl" && a.some(x => /^-p?i/.test(x)))) return R(5, "Editing " + (place(pos[pos.length - 1]) || "a file"), "edit");
    if (c === "mkdir" || c === "md" || c === "new-item") return R(5, "Creating " + (place(pos[0]) || "a folder"), "edit");
    if (c === "touch") return R(5, "Creating " + (place(pos[0]) || "a file"), "edit");
    if (/^(rm|del|erase|rmdir|rd|remove-item|unlink|trash)$/.test(c)) return R(5, "Deleting " + (pos.length > 1 ? pos.length + " files" : place(pos[0]) || "files"), "edit");
    if (c === "mv" || c === "move" || c === "ren" || c === "rename" || c === "move-item") return R(5, (pos.length === 2 && place(pos[1]) !== "the project folder" && !/[\\/]$/.test(pos[1]) && place(pos[0]) !== place(pos[1]) && !pos[1].includes("/") ? "Renaming " : "Moving ") + (place(pos[0]) || "files"), "edit");
    if (c === "cp" || c === "copy" || c === "xcopy" || c === "robocopy" || c === "rsync" || c === "copy-item") return R(5, "Copying " + (place(pos[0]) || "files"), "edit");
    if (c === "ln") return R(4, "Linking " + (place(pos[0]) || "a file"), "edit");
    if (c === "chmod" || c === "chown" || c === "icacls") return R(4, "Changing permissions on " + (place(pos[pos.length - 1]) || "a file"), "edit");
    if (c === "tee" && pos[0]) return R(5, "Writing " + place(pos[0]), "write");
    if (c === "unzip" || (c === "7z" && sub === "x") || c === "expand-archive" || (c === "tar" && a.some(x => /^-?[a-z]*x/.test(x)))) return R(5, "Unpacking " + (place(pos.find(x => /\.(zip|tar|gz|tgz|xz|7z|bz2|zst)$/.test(x))) || "an archive"), "edit");
    if (c === "zip" || (c === "tar" && a.some(x => /^-?[a-z]*c/.test(x))) || c === "compress-archive") return R(5, "Packing files into an archive", "edit");
    if (redirect) return R(5, "Writing " + place(redirect), "write");

    // Network.
    if (/^(curl|wget|http|https|xh|invoke-webrequest|iwr|invoke-restmethod|irm)$/.test(c)) {
      const u = a.find(x => /^https?:\/\//i.test(x)) || pos.find(x => /\.[a-z]{2,}(\/|$|:)/i.test(x) || /^localhost/.test(x)) || "";
      const hh = host(u);
      const dl = a.some(x => /^(-o|-O|--output|--remote-name|-outfile)$/i.test(x)) || c === "wget";
      return R(5, (dl ? "Downloading from " : "Fetching ") + (hh || "a web page"), "fetch", hh);
    }
    if (c === "ping" || c === "traceroute" || c === "tracert" || c === "test-connection") return R(4, "Checking the connection to " + (host(pos[0]) || pos[0] || "the internet"), "check");
    if (/^(nslookup|dig|host)$/.test(c)) return R(3, "Looking up " + (pos[0] || "an address"), "check");
    if (c === "ssh" || c === "mosh") return R(5, "Connecting to " + ((pos[0] || "").split("@").pop() || "a server"));
    if (c === "scp" || c === "sftp") return R(5, "Copying files to a server", "edit");

    // Containers and services.
    if (c === "docker" || c === "podman" || c === "docker-compose") {
      const s = c === "docker-compose" ? "compose" : sub, s2 = c === "docker-compose" ? sub : (pos[1] || "");
      if (s === "compose") return R(6, s2 === "up" ? "Starting the containers" : s2 === "down" ? "Stopping the containers" : s2 === "logs" ? "Checking container logs" : s2 === "ps" ? "Checking Docker containers" : s2 === "build" ? "Building the containers" : "Managing the containers");
      const D = { ps: "Checking Docker containers", images: "Checking Docker images", run: "Starting a container", start: "Starting a container", stop: "Stopping a container", rm: "Removing a container", logs: "Checking container logs", exec: "Running a command in a container", pull: "Downloading a Docker image", push: "Uploading a Docker image", inspect: "Checking a container", info: "Checking Docker", version: "Checking Docker" };
      return R(5, D[s] || "Using Docker");
    }
    if (c === "systemctl" || c === "service" || c === "launchctl" || c === "sc") {
      const act = c === "service" ? pos[1] : sub, svc = c === "service" ? pos[0] : pos[1];
      const GER = { start: "Starting", stop: "Stopping", restart: "Restarting", reload: "Reloading", enable: "Enabling", disable: "Disabling" };
      if (GER[act]) return R(5, `${GER[act]} ${place(svc) || "a service"}`);
      return R(3, "Checking " + (place(svc) || "a service"), "check");
    }
    if (c === "journalctl" || c === "dmesg" || c === "get-eventlog" || c === "get-winevent") return R(3, "Reading the system logs", "read");
    if (/^(open|xdg-open|start|explorer|invoke-item|ii)$/.test(c)) return R(4, "Opening " + (host(pos[0]) && /^https?:/.test(pos[0] || "") ? host(pos[0]) : place(pos[0]) || "it"), "open");
    if (c === "code" || c === "cursor" || c === "subl" || c === "vim" || c === "nvim" || c === "nano") return R(4, "Opening " + (place(pos[0], "folder") || "the editor"), "open");
    if (c === "ffmpeg" || c === "magick" || c === "convert") return R(5, "Converting media");
    if (c === "sqlite3" || c === "psql" || c === "mysql") return R(5, "Querying the database");
    if (c === "kubectl" || c === "helm") return R(5, "Checking the cluster");
    if (c === "terraform") return R(5, sub === "apply" ? "Applying infrastructure changes" : "Checking infrastructure");
    if (c === "env" || c === "printenv" || c === "get-childitem env:") return R(1, "Checking environment settings", "check");
    if (c === "history") return R(1, "Checking recent commands", "check");
    if (c === "crontab" || c === "schtasks") return R(4, "Checking scheduled tasks", "check");
    if (c === "ollama") return R(5, sub === "pull" ? "Downloading a model" : sub === "run" ? "Running a local model" : "Checking local models");
    return R(1, "", "other");
  }

  // The whole command line → one phrase for its main intent.
  function describeShell(cmd) {
    cmd = String(cmd || "").replace(/\r/g, "");
    // Heredoc bodies are file contents, not commands.
    cmd = cmd.replace(/<<-?\s*(['"]?)(\w+)\1[^\n]*\n[\s\S]*?\n\s*\2\s*(\n|$)/g, (m) => m.split("\n")[0] + "\n");
    cmd = cmd.replace(/\\\n/g, " ").replace(/^\s*(\$|>|PS [^>]*>)\s*/, "");
    const segs = segments(cmd).map(part);
    if (!segs.length) return "Running a command";
    let best = segs[0];
    segs.forEach(s => { if (s.p > best.p) best = s; });
    if (!best.t || best.p < 0.5) {
      const cd = segs.find(s => s.k === "cd");
      return cd && segs.length === 1 ? cd.t : "Running a command";
    }
    // Several reads or listings: "Reading a.md and b.md", "Reading a.md and 2 more files".
    if (best.k === "read" || best.k === "list") {
      const same = segs.filter(s => s.k === best.k && s.p === best.p && s.x);
      const names = [...new Set(same.map(s => s.x))];
      if (names.length === 2) return clip((best.k === "read" ? "Reading " : "Looking through ") + names[0] + " and " + names[1]);
      if (names.length > 2) return clip((best.k === "read" ? "Reading " : "Looking through ") + names[0] + ` and ${names.length - 1} more ${best.k === "read" ? "files" : "folders"}`);
    }
    return clip(best.t);
  }

  // ---------- every tool ----------
  const CONN = {
    gmail_search: "Searching your email", gmail_read: "Reading an email", gmail_draft: "Drafting an email", gmail_send: "Sending an email",
    gcal_events: "Checking your calendar", gcal_create: "Adding a calendar event", drive_search: "Searching your Google Drive", drive_read: "Reading a Drive file", gcontacts_search: "Looking up a contact",
    github_my_repos: "Checking your GitHub repos", github_my_prs: "Checking your pull requests", github_issues: "Checking GitHub issues", github_read: "Reading a GitHub issue", github_create_issue: "Opening a GitHub issue", github_comment: "Commenting on GitHub", github_notifications: "Checking GitHub notifications", github_search_repos: "Searching GitHub",
    notion_search: "Searching Notion", notion_read: "Reading a Notion page", notion_append: "Adding to a Notion page", notion_create_page: "Creating a Notion page",
    slack_channels: "Checking your Slack channels", slack_read: "Reading Slack", slack_post: "Posting to Slack",
    spotify_now_playing: "Checking what's playing", spotify_play: "Playing music on Spotify", spotify_control: "Controlling Spotify", spotify_search: "Searching Spotify",
    suggest_connector: "Suggesting an account to connect",
    read_screen: "Reading your screen", see_screen: "Looking at your screen", notify: "Sending a notification", screenshot: "Taking a screenshot",
    click: "Clicking on screen", type_text: "Typing", press_key: "Pressing a key", scroll: "Scrolling", move_mouse: "Moving the mouse", focus_window: "Switching windows", list_windows: "Checking open windows",
    remember: "Saving that to memory", forget: "Updating memory", schedule: "Setting up a schedule", use_skill: "Following a saved skill", write_skill: "Saving a skill", system_info: "Checking your computer",
    job_log: "Checking a background job", job_input: "Sending input to a background job", job_stop: "Stopping a background job"
  };
  const isPlain = s => !!s && typeof s === "string" && s.length <= 70 && !/[{}<>$|;`\\=]|&&|\s--?\w|~\/|\/\w+\/|\bhttps?:|^\W/.test(s) && /^[A-Z]/i.test(s.trim());

  function parseArgs(args) {
    if (args && typeof args === "object") return args;
    try { const o = JSON.parse(args || "{}"); return o && typeof o === "object" ? o : {}; } catch (e) { return {}; }
  }

  function describe(name, args) {
    const a = parseArgs(args);
    const v = k => String(a[k] ?? "").split("\n")[0].trim();
    const file = () => place(v("path") || v("file") || v("filename")) || "a file";
    const summary = isPlain(v("summary")) ? clip(cap(v("summary"))) : "";
    let s = "";
    switch (name) {
      case "run_shell": case "shell": case "bash": case "run_command": case "terminal": case "exec":
        s = describeShell(a.command || a.cmd || a.script || "");
        if (a.background && /^Running (?!the tests|a command)/.test(s)) s = s.replace(/^Running /, "Starting ");
        if (s === "Running a command" && summary) s = summary;
        else if (s === "Running a command" && a.background) s = "Starting a background job";
        return s;
      case "file_read": return "Reading " + file();
      case "file_edit": return "Editing " + file();
      case "file_write": return "Writing " + file();
      case "file_delete": return "Deleting " + file();
      case "file_list": return "Looking through " + place(v("path"), "folder");
      case "file_search": { const t = v("pattern") || v("query"); return t ? clip(`Searching the files for "${clip(t, 30)}"`) : "Searching the files"; }
      case "web_search": return v("query") ? clip(`Searching the web for "${clip(v("query"), 36)}"`) : "Searching the web";
      case "fetch_page": return "Reading " + (host(v("url")) || "a web page");
      case "http_request": return "Calling " + (host(v("url")) || "a web service");
      case "phone_ask": return v("task") ? clip(`Asking your phone: ${clip(v("task"), 40)}`) : "Asking your phone";
      case "download_file": return "Downloading from " + (host(v("url")) || "the web");
      case "open_url": { const t = v("target") || v("url"); return "Opening " + (/^https?:/.test(t) ? host(t) : place(t) || "a page"); }
      case "git": return describeShell("git " + (v("args") || v("command") || "status"));
      case "run_code": {
        const lang = v("language").toLowerCase();
        const L = { python: "Python", py: "Python", node: "JavaScript", javascript: "JavaScript", js: "JavaScript", typescript: "TypeScript", ts: "TypeScript", bash: "a shell", sh: "a shell", powershell: "a PowerShell" };
        return summary || `Running a ${L[lang] || "quick"} script`;
      }
      case "update_blaze": return "Checking for a Blaze update";
      case "install_tool": return "Installing " + (place(v("name")) || "a tool");
      case "connector_tools": { const g = v("group").replace(/^mcp:/, ""); return g ? `Getting ${cap(words(g))} ready` : "Getting your accounts ready"; }
    }
    if (CONN[name]) return CONN[name];
    // MCP tools are mcp__<server>__<tool> (v0.8.1; mcp_<server>_<tool> before): "Using Linear: list issues".
    const m = /^mcp__([a-z0-9-]+)__(.+)$/i.exec(name || "") || /^mcp_([a-z0-9]+)_(.+)$/i.exec(name || "");
    if (m) return summary || clip(`Using ${cap(words(m[1]))}: ${words(m[2])}`);
    return summary || (name ? clip("Using " + words(name)) : "Working on it");
  }

  // The exact call, for the expanded row (mono). Shell: the command itself.
  function detail(name, args) {
    const a = parseArgs(args);
    if (/^(run_shell|shell|bash|run_command|terminal|exec)$/.test(name || "")) return String(a.command || a.cmd || a.script || "");
    if (name === "git") return "git " + String(a.args || a.command || "");
    if (name === "run_code") return String(a.code || "");
    let body = "";
    try { body = typeof args === "string" ? JSON.stringify(JSON.parse(args || "{}"), null, 2) : JSON.stringify(a, null, 2); } catch (e) { body = String(args || ""); }
    return `${name} ${body === "{}" ? "" : body}`.trim();
  }
  const detailLabel = name => /^(run_shell|shell|bash|run_command|terminal|exec|git)$/.test(name || "") ? "Command" : name === "run_code" ? "Code" : "Tool call";

  // Backend progress lines ("Running: cat x", "git log", "pip install y") → plain words, or
  // "" to keep the current phrase. Lines already in plain words pass through.
  function plainStatus(line) {
    const t = String(line || "").split("\n")[0].trim();
    if (!t) return "";
    let m;
    if ((m = /^Running:\s*([\s\S]*)$/.exec(t))) return describeShell(m[1]);
    if ((m = /^Job \d+ started:\s*([\s\S]*)$/.exec(t))) { const d = describeShell(m[1]); return d === "Running a command" ? "Starting a background job" : d.replace(/^Running /, "Starting "); }
    if (/^git\s/.test(t)) return describeShell(t);
    if (/^(pip|npm) install\b/.test(t)) return "Installing dependencies";
    if (/^Creating a Python venv/.test(t)) return "Setting up Python";
    if (/^Running (Python|Node)$/.test(t)) return t === "Running Python" ? "Running a Python script" : "Running a JavaScript script";
    if ((m = /^Searching:\s*(.*)$/.exec(t))) return m[1] ? clip(`Searching the web for "${clip(m[1], 36)}"`) : "Searching the web";
    if ((m = /^(Reading|Downloading|GET|POST|PUT|PATCH|DELETE|HEAD)\s+(https?:\/\/\S+)/.exec(t))) return (m[1] === "Reading" ? "Reading " : m[1] === "Downloading" ? "Downloading from " : "Calling ") + host(m[2]);
    if ((m = /^(Wrote|Edited)\s+(.+)$/.exec(t))) return (m[1] === "Wrote" ? "Writing " : "Editing ") + place(m[2]);
    if (/^Installing\s/.test(t)) return clip(t);
    return isPlain(t) ? clip(t) : "";
  }

  // "Worked · 6 steps · 14s"
  function workedLine(n, secs) {
    const parts = ["Worked", `${n} step${n === 1 ? "" : "s"}`];
    // Under a second says nothing worth reading ("<1s"), so it is left off.
    if (secs != null && isFinite(secs) && secs >= 1) parts.push(secs >= 60 ? `${Math.floor(secs / 60)}m ${Math.round(secs % 60)}s` : `${Math.round(secs)}s`);
    return parts.join(" · ");
  }

  const api = { describe, describeShell, detail, detailLabel, plainStatus, workedLine, isPlain, place };
  root.STEPS = api;
  if (typeof module === "object" && module.exports) module.exports = api;
})(typeof window !== "undefined" ? window : globalThis);
