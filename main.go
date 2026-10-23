// Blaze Desktop: Blaze, the Android assistant, as an app for Windows, macOS and Linux.
package main

import (
	"context"
	"embed"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"blaze-desktop/internal/config"
	"blaze-desktop/internal/llm"
	"blaze-desktop/internal/sched"
	"blaze-desktop/internal/screen"
	"blaze-desktop/internal/sysinfo"
	"blaze-desktop/internal/tools"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var icon []byte

var version = "dev"

func main() {
	if len(os.Args) > 2 && os.Args[1] == "run-job" {
		code := runJob(os.Args[2])
		jobNotify(os.Args[2], code)
		os.Exit(code)
	}
	if len(os.Args) > 1 && os.Args[1] == "__screen" {
		os.Exit(screen.HelperMain(os.Args[2:])) // the screen-reading helper (internal/screen)
	}
	app := NewApp()
	app.flags = parseFlags(os.Args[1:])
	if app.flags.relaunch {
		// An update restarted us: give the old copy time to quit and free the single-instance lock.
		time.Sleep(2500 * time.Millisecond)
	}
	err := wails.Run(&options.App{
		Title:            "Blaze",
		Width:            1180,
		Height:           780,
		MinWidth:         760,
		MinHeight:        520,
		BackgroundColour: &options.RGBA{R: 252, G: 252, B: 252, A: 255},
		AssetServer:      &assetserver.Options{Assets: assets, Handler: csvChartHandler(mediaHandler())}, // /media/*: Music + Video files (media_app.go); /csvkit/chart/*: CSV report charts (csv_app.go)
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		OnBeforeClose:    func(ctx context.Context) bool { return app.beforeClose(ctx) },
		StartHidden:      app.flags.background,
		Frameless:        runtime.GOOS == "windows", // custom title-bar buttons in #top
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "com.blaze.desktop",
			OnSecondInstanceLaunch: func(d options.SecondInstanceData) { go app.secondInstance(d.Args) },
		},
		DragAndDrop: &options.DragAndDrop{EnableFileDrop: true}, // files dropped on the window attach to the message
		Bind:        []interface{}{app},
		Windows:     &windows.Options{Theme: windows.SystemDefault},
		Mac: &mac.Options{
			TitleBar: mac.TitleBarHiddenInset(),
			About:    &mac.AboutInfo{Title: "Blaze", Message: "Blaze " + version, Icon: icon},
			OnUrlOpen: func(u string) {
				go func() { app.waitReady(); app.routeURL(u) }()
			},
		},
		Linux: &linux.Options{Icon: icon, ProgramName: "blaze"},
	})
	if err != nil {
		fmt.Println("Error:", err)
	}
}

// runJob is what the OS scheduler calls for jobs made in the app.
func runJob(name string) int {
	j, ok := sched.Get(name)
	if !ok {
		return 2
	}
	if j.Once() {
		defer sched.Remove(name) // one-time reminders don't come back next year
	}
	log, err := sched.LogWriter(name)
	if err != nil {
		return 1
	}
	defer log.Close()
	fmt.Fprintf(log, "\n=== %s ===\n", time.Now().Format("2006-01-02 15:04:05"))
	info := sysinfo.Detect()
	dir := j.Dir
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		dir = info.Home
	}
	if j.Command != "" {
		r := tools.NewShell(info.Shell, info.ShellPath, dir).Run(j.Command, 2*time.Hour)
		fmt.Fprintln(log, r)
		if strings.HasPrefix(r, "exit 0") {
			return 0
		}
		return 1
	}
	cfg := config.Load()
	env := &tools.Env{Info: &info, Root: dir, Shell: tools.NewShell(info.Shell, info.ShellPath, dir), Jobs: &tools.Jobs{}}
	env.Confirm = func(t, d string) bool { fmt.Fprintf(log, "[declined, nobody to ask] %s %s\n", t, d); return false }
	defer env.Jobs.StopAll()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	hist := []llm.Message{{Role: "system", Content: "You are Blaze running a scheduled job unattended on the user's PC. Do the task with your tools, then report the result briefly.\n\nPC PROFILE\n" + info.Summary()}, {Role: "user", Content: j.Prompt}}
	for step := 0; step < 30; step++ {
		r, err := llm.Complete(ctx, cfg, hist, tools.All(), nil)
		if err != nil {
			fmt.Fprintln(log, "Error:", err)
			return 1
		}
		n := llm.NormalizeCalls(r.ToolCalls, tools.Names())
		hist = append(hist, llm.Message{Role: "assistant", Content: r.Text, ToolCalls: n.Calls})
		if len(n.Dropped) > 0 {
			fmt.Fprintln(log, "[skipped malformed tool calls]", strings.Join(n.Dropped, "; "))
		}
		if len(n.Calls) == 0 {
			fmt.Fprintln(log, r.Text)
			return 0
		}
		for _, tc := range n.Calls {
			hist = append(hist, llm.Message{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: tools.Run(env, tc.Function.Name, tc.Function.Arguments)})
		}
	}
	return 1
}
