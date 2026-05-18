package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/jonasross/canopy/aggregator"
	"github.com/jonasross/canopy/internal/demo"
	"github.com/jonasross/canopy/pr"
	"github.com/jonasross/canopy/sessions"
	"github.com/jonasross/canopy/tui"
)

var (
	demoScript   string
	demoWidth    int
	demoHeight   int
	demoKeepTmp  bool
	demoFreezeIn string // override path to the `freeze` binary (test seam)
)

var demoCmd = &cobra.Command{
	Use:   "demo",
	Short: "Launch canopy against a throwaway sandbox repository",
	Long: `demo builds a fresh tmpdir with a real git repo, synthetic Claude
sessions, and a canned PR list, then runs the TUI against it.

Without --script, demo runs the TUI interactively against the sandbox.
With --script, demo replays a directive file and writes the captured frames
to the paths the script names; the TUI is not displayed.

Destructive ops (worktree remove, kill, open URL, shell drop) are
soft-gated when CANOPY_DEMO=1 — even an automated script can't escape the
sandbox.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()

		fix, err := demo.Build("")
		if err != nil {
			return fmt.Errorf("canopy demo: build fixture: %w", err)
		}
		if demoKeepTmp {
			fmt.Fprintf(cmd.ErrOrStderr(), "demo fixture: %s (--keep-tmp set; not cleaning up)\n", fix.Root)
		} else {
			defer func() { _ = fix.Cleanup() }()
		}

		os.Setenv("CANOPY_DEMO", "1")
		defer os.Unsetenv("CANOPY_DEMO")

		// Swap the gh exec seam for a fixture-JSON reader so pr.List
		// returns the canned states without needing a real GitHub auth.
		// Also stub LookPath: pr.List checks gh-on-PATH before invoking
		// the run seam, so on machines without gh installed (CI hosts,
		// demo viewers) we'd otherwise get ErrNoGH and lose PR columns.
		prJSON, err := fix.PRFixtureBytes()
		if err != nil {
			return fmt.Errorf("canopy demo: read PR fixture: %w", err)
		}
		restoreRun := pr.SetRunCmd(func(_ context.Context, _, _ string, _ ...string) ([]byte, error) {
			return prJSON, nil
		})
		defer pr.SetRunCmd(restoreRun)
		restoreLook := pr.SetLookPath(func(name string) (string, error) {
			return "/usr/bin/" + name, nil
		})
		defer pr.SetLookPath(restoreLook)

		store, err := sessions.Open(fix.SessionsRoot)
		if err != nil {
			return fmt.Errorf("canopy demo: open sessions: %w", err)
		}
		defer store.Close()

		agg, err := aggregator.New(aggregator.Config{
			Repos:        []aggregator.Repo{{Root: fix.RepoRoot, Name: filepath.Base(fix.RepoRoot)}},
			SessionStore: store,
			PRCache:      pr.NewCache(30 * time.Second),
		})
		if err != nil {
			return fmt.Errorf("canopy demo: create aggregator: %w", err)
		}
		if err := agg.Start(ctx); err != nil {
			return fmt.Errorf("canopy demo: start aggregator: %w", err)
		}

		if demoScript == "" {
			return tui.Run(ctx, agg)
		}
		return runScript(ctx, cmd, agg, demoScript, demoWidth, demoHeight)
	},
}

func init() {
	demoCmd.Flags().StringVar(&demoScript, "script", "", "replay a script of directives; capture frames as the script directs")
	demoCmd.Flags().IntVar(&demoWidth, "width", 140, "terminal width in cells")
	demoCmd.Flags().IntVar(&demoHeight, "height", 40, "terminal height in cells")
	demoCmd.Flags().BoolVar(&demoKeepTmp, "keep-tmp", false, "leave the demo tmpdir in place after exit")
	demoCmd.Flags().StringVar(&demoFreezeIn, "freeze-bin", "freeze", "name or path of the freeze binary used for capture-png")

	rootCmd.AddCommand(demoCmd)
}

// freezeAvailable reports whether the freeze binary is on PATH (or absolute).
func freezeAvailable() bool {
	if filepath.IsAbs(demoFreezeIn) {
		_, err := os.Stat(demoFreezeIn)
		return err == nil
	}
	_, err := exec.LookPath(demoFreezeIn)
	return err == nil
}

// renderPNG shells out to `freeze` to turn an ANSI input file into a PNG.
// Surfaces a friendly install hint when freeze is missing. The `ansi`
// language tells freeze to render raw ANSI escapes rather than try to
// syntax-highlight the content as code.
func renderPNG(ctx context.Context, ansiPath, pngPath string) error {
	if !freezeAvailable() {
		return fmt.Errorf("freeze binary %q not on PATH; install with: go install github.com/charmbracelet/freeze@latest", demoFreezeIn)
	}
	c := exec.CommandContext(ctx, demoFreezeIn, "--language", "ansi", "--output", pngPath, ansiPath)
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("freeze: %w: %s", err, string(out))
	}
	return nil
}
