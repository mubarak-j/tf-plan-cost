package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/mubarak-j/tf-plan-cost/internal/plan"
)

// runStrip writes a copy of the plan with only what pricing needs, so it can
// be uploaded to the tf-plan-cost App without the plan's secrets.
func runStrip(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("strip", flag.ContinueOnError)
	fs.SetOutput(stderr)
	output := fs.String("o", "-", "output file (- for stdout)")
	fs.Usage = func() {
		fmt.Fprint(stderr, `Usage: tf-plan-cost strip [flags] plan.json

Writes the plan reduced to what pricing needs: resource changes with
sensitive values removed, provider regions and the variables they use.

Flags:
`)
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

	in := stdin
	if path := positional[0]; path != "-" {
		f, err := os.Open(path)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		defer f.Close()
		in = f
	}

	out := stdout
	var file *os.File
	if *output != "-" {
		file, err = os.Create(*output)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		out = file
	}
	err = plan.Strip(in, out)
	if file != nil {
		if cerr := file.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "error: %s: %v\n", positional[0], err)
		return 1
	}
	return 0
}
