// Command tf-plan-cost estimates the monthly AWS cost change of a Terraform plan.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/mubarak-j/tf-plan-cost/internal/plan"
	"github.com/mubarak-j/tf-plan-cost/internal/pricing"
	"github.com/mubarak-j/tf-plan-cost/internal/pricing/resources"
	"github.com/mubarak-j/tf-plan-cost/internal/render"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `tf-plan-cost estimates the monthly AWS cost change of a Terraform plan.

Usage:
  terraform plan -out=tfplan && terraform show -json tfplan > plan.json
  tf-plan-cost diff [flags] plan.json     (use - to read from stdin)
  tf-plan-cost github-comment --report report.json [flags]
  tf-plan-cost strip [-o stripped.json] plan.json
  tf-plan-cost serve                      (GitHub App server)
  tf-plan-cost version

Run 'tf-plan-cost <command> -h' for flags.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, newPriceClient)
	stop()
	os.Exit(code)
}

// clientFactory builds the PriceClient; tests replace it with a fake.
type clientFactory func(ctx context.Context, cacheDir string, noCache bool) (pricing.PriceClient, error)

func newPriceClient(ctx context.Context, cacheDir string, noCache bool) (pricing.PriceClient, error) {
	aws, err := pricing.NewAWSClient(ctx)
	if err != nil {
		return nil, err
	}
	if noCache {
		return aws, nil
	}
	if cacheDir == "" {
		// No usable cache dir (e.g. no $HOME): fall back to memory only.
		cacheDir, _ = pricing.DefaultCacheDir()
	}
	return pricing.NewCache(aws, cacheDir, pricing.DefaultCacheTTL), nil
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, newClient clientFactory) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "diff":
		return runDiff(ctx, args[1:], stdin, stdout, stderr, newClient)
	case "github-comment":
		return runGitHubComment(ctx, args[1:], stdout, stderr)
	case "strip":
		return runStrip(args[1:], stdin, stdout, stderr)
	case "serve":
		return runServe(ctx, args[1:], os.Getenv, stderr, newClient, nil)
	case "version", "--version":
		fmt.Fprintln(stdout, "tf-plan-cost", version)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

func runDiff(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, newClient clientFactory) int {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	region := fs.String("region", envRegion(), "AWS region for resources whose region is not in the plan (default $AWS_REGION or $AWS_DEFAULT_REGION)")
	format := fs.String("format", "markdown", "output format: markdown or json")
	noCache := fs.Bool("no-cache", false, "do not read or write the price cache")
	cacheDir := fs.String("cache-dir", "", "price cache directory (default: user cache dir)")
	fs.Usage = func() {
		fmt.Fprint(stderr, "Usage: tf-plan-cost diff [flags] plan.json\n\nFlags:\n")
		fs.PrintDefaults()
	}

	positional, err := parseInterspersed(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		fs.Usage()
		return 2
	}
	if *format != "markdown" && *format != "json" {
		fmt.Fprintf(stderr, "invalid --format %q (want markdown or json)\n", *format)
		return 2
	}

	p, err := readPlan(positional[0], stdin)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	client, err := newClient(ctx, *cacheDir, *noCache)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	est := pricing.Estimator{Client: client, Pricers: resources.Registry(), DefaultRegion: *region}
	report, err := est.Estimate(ctx, p)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		fmt.Fprintln(stderr, "hint: tf-plan-cost reads prices from the AWS Price List API and needs AWS credentials allowed to call pricing:GetProducts.")
		return 1
	}

	if *format == "json" {
		err = render.JSON(stdout, report)
	} else {
		err = render.Markdown(stdout, report, render.Options{})
	}
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}

func readPlan(path string, stdin io.Reader) (*plan.Plan, error) {
	if path == "-" {
		return plan.Parse(stdin)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	p, err := plan.Parse(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

// parseInterspersed lets flags appear before or after positional arguments.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return positional, nil
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
}

func envRegion() string {
	if r := os.Getenv("AWS_REGION"); r != "" {
		return r
	}
	return os.Getenv("AWS_DEFAULT_REGION")
}
