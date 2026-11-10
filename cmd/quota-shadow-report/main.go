// Command quota-shadow-report analyzes the quota shadow log written by
// routing.quota-shadow (see docs/quota-shadow.md) and prints a plain-text
// report: agreement between the real and the shadow pick, soft-limited picks,
// outages, window semantics, per-credential utilization with perished quota,
// and a replay of round-robin, fill-first, and the shadow policy when the log
// supports one.
//
// Usage:
//
//	go run ./cmd/quota-shadow-report [flags] [file ...]
//
// Without file arguments it reads -file, preceded by its rotation <file>.1
// when present. Explicit files are read in the order given.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run parses flags, analyzes the log, and writes the report. It returns the
// process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("quota-shadow-report", flag.ContinueOnError)
	flags.SetOutput(stderr)
	file := flags.String("file", "quota-shadow.jsonl", "shadow log to read when no file arguments are given")
	noRotated := flags.Bool("no-rotated", false, "do not read <file>.1 before -file")
	tolerance := flags.Duration("reset-tolerance", 5*time.Minute, "reset_at_ms changes up to this size count as the same reset; widened to the reset rounding when coarser")
	timeline := flags.Bool("timeline", true, "print every observed window per credential in section 5")
	strategy := flags.String("strategy", policyRoundRobin, "routing strategy that produced the log (round-robin or fill-first), used to calibrate the replay")
	softLimit := flags.Float64("soft-limit-percent", 90, "routing.quota-shadow.soft-limit-percent used by the replayed shadow policy")
	minSamples := flags.Int("min-cost-samples", 20, "consecutive same-window observations needed to estimate a credential's cost per request")
	flags.Usage = func() {
		_, _ = fmt.Fprintf(stderr, "Usage: quota-shadow-report [flags] [file ...]\n\n")
		flags.PrintDefaults()
	}
	if errParse := flags.Parse(args); errParse != nil {
		if errors.Is(errParse, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	opts := options{
		TolMs:    tolerance.Milliseconds(),
		Timeline: *timeline,
		Replay: replayOptions{
			Strategy:       *strategy,
			SoftLimit:      *softLimit / 100,
			MinCostSamples: *minSamples,
		},
	}
	if errValidate := validateOptions(opts); errValidate != nil {
		_, _ = fmt.Fprintf(stderr, "quota-shadow-report: %v\n", errValidate)
		return 2
	}
	paths, errResolve := resolveInputs(*file, flags.Args(), !*noRotated)
	if errResolve != nil {
		_, _ = fmt.Fprintf(stderr, "quota-shadow-report: %v\n", errResolve)
		return 1
	}
	ds, errLoad := loadDataset(paths)
	if errLoad != nil {
		_, _ = fmt.Fprintf(stderr, "quota-shadow-report: %v\n", errLoad)
		return 1
	}
	if errRender := buildReport(ds, opts).render(stdout); errRender != nil {
		_, _ = fmt.Fprintf(stderr, "quota-shadow-report: write report: %v\n", errRender)
		return 1
	}
	return 0
}

func validateOptions(opts options) error {
	if opts.Replay.Strategy != policyRoundRobin && opts.Replay.Strategy != policyFillFirst {
		return fmt.Errorf("-strategy must be %s or %s, got %q", policyRoundRobin, policyFillFirst, opts.Replay.Strategy)
	}
	if opts.Replay.SoftLimit <= 0 || opts.Replay.SoftLimit > 1 {
		return fmt.Errorf("-soft-limit-percent must be in (0, 100]")
	}
	if opts.Replay.MinCostSamples < 1 {
		return fmt.Errorf("-min-cost-samples must be at least 1")
	}
	if opts.TolMs < 0 {
		return fmt.Errorf("-reset-tolerance must not be negative")
	}
	return nil
}

// resolveInputs returns the files to read, oldest first.
func resolveInputs(file string, args []string, withRotated bool) ([]string, error) {
	if len(args) > 0 {
		return args, nil
	}
	if file == "" {
		return nil, errors.New("no input file")
	}
	paths := []string{file}
	if withRotated {
		rotated := file + ".1"
		info, errStat := os.Stat(rotated)
		switch {
		case errStat == nil && !info.IsDir():
			paths = []string{rotated, file}
		case errStat != nil && !errors.Is(errStat, os.ErrNotExist):
			return nil, fmt.Errorf("stat %s: %w", rotated, errStat)
		}
	}
	return paths, nil
}
