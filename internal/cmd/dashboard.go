package cmd

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/wolzey/claude-manager/internal/config"
	"github.com/wolzey/claude-manager/internal/dashboard"
	"github.com/wolzey/claude-manager/internal/dashboard/web"
	"github.com/wolzey/claude-manager/internal/store"
)

func dashboardCmd() *cobra.Command {
	var port int
	var host string
	var open bool
	var noWatch bool

	c := &cobra.Command{
		Use:   "dashboard",
		Short: "Start the local browser dashboard for live cmgr status",
		Long:  "Starts an HTTP server (default http://127.0.0.1:7777) serving a live dashboard of every project, every worker, and recent inbox events. Use --open to launch the browser automatically.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _ := config.Load()
			if port == 0 {
				port = cfg.DashboardPort
			}
			if host == "" {
				host = cfg.DashboardHost
			}
			return runDashboard(host, port, open, noWatch, cfg.WatchDebounceMS)
		},
	}
	c.Flags().IntVar(&port, "port", 0, "TCP port to bind (default 7777, from config)")
	c.Flags().StringVar(&host, "host", "", "host to bind (default 127.0.0.1, from config)")
	c.Flags().BoolVar(&open, "open", false, "open the dashboard in the default browser on start")
	c.Flags().BoolVar(&noWatch, "no-watch", false, "disable the fsnotify watcher (UI uses polling)")
	return c
}

func runDashboard(host string, port int, openBrowser, noWatch bool, debounceMS int) error {
	w := dashboard.NewWatcher(time.Duration(debounceMS) * time.Millisecond)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if !noWatch {
		go func() {
			if err := w.Run(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "[cmgr] watcher: %v\n", err)
			}
		}()
	}

	srv := dashboard.NewServer(w, web.Assets())
	srv.SetApprover(&dashboardApprover{})
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	url := fmt.Sprintf("http://%s/", addr)
	fmt.Fprintf(os.Stderr, "[cmgr] dashboard listening on %s\n", url)

	if openBrowser {
		go func() {
			time.Sleep(150 * time.Millisecond)
			if err := openInBrowser(url); err != nil {
				fmt.Fprintf(os.Stderr, "[cmgr] could not open browser: %v\n", err)
			}
		}()
	}

	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// dashboardApprover satisfies dashboard.PlanApprover by reusing the same
// approve/reject prompts and runSend path that `cmgr plan` uses. Kept in
// internal/cmd to avoid an import cycle (dashboard ↘ cmd would loop back via
// the runner and store packages).
type dashboardApprover struct{}

func (dashboardApprover) Approve(slug, worker, extraContext string, persist bool) error {
	prompt := approvalPromptTmpl
	if extraContext = strings.TrimSpace(extraContext); extraContext != "" {
		prompt += "\n\nAdditional context: " + extraContext
	}
	if err := runSend(slug, worker, prompt, false, 0, "", "acceptEdits"); err != nil {
		return err
	}
	if persist {
		w, _ := store.LoadWorker(slug, worker)
		if w != nil {
			w.DefaultMode = "acceptEdits"
			_ = store.SaveWorker(slug, w)
		}
	}
	return nil
}

func (dashboardApprover) Reject(slug, worker, feedback string) error {
	prompt := fmt.Sprintf(rejectionPromptTmpl, feedback)
	return runSend(slug, worker, prompt, false, 0, "", "plan")
}

func openInBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
	return cmd.Start()
}
