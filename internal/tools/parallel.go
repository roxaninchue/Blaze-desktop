package tools

// parallelSafe are tools that only read, with no approval prompt and no shared state,
// so several of them from one model reply can run at the same time (the Blaze CLI's
// list, by Desktop names). Writes, commands, git, installs, schedules, anything that
// asks for approval and system_info (it refreshes the shared PC profile) are left out:
// they run one by one and end a run of reads.
var parallelSafe = map[string]bool{
	"file_read": true, "file_list": true, "file_search": true,
	"web_search": true, "fetch_page": true, "get_weather": true, "recall": true,
	"load_skill": true, "use_skill": true, "job_log": true,
}

// ParallelSafe reports whether a call can run beside other read-only calls.
func ParallelSafe(name string) bool { return parallelSafe[name] }
