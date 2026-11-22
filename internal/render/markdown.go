// Package render formats cost reports for PR comments and machines.
package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/mubarak-j/tf-plan-cost/internal/plan"
	"github.com/mubarak-j/tf-plan-cost/internal/pricing"
)

// DefaultCommentID is the comment id used when a repository has a single
// Terraform root.
const DefaultCommentID = "default"

// Marker returns the hidden HTML comment that starts tf-plan-cost's PR
// comment for a given comment id, so later runs update that comment instead
// of posting a new one. Each Terraform root in a monorepo can use its own id.
// The id must be valid per ValidCommentID.
func Marker(commentID string) string {
	if commentID == "" || commentID == DefaultCommentID {
		return "<!-- tf-plan-cost -->"
	}
	return "<!-- tf-plan-cost:" + commentID + " -->"
}

// ValidCommentID reports whether id is safe to embed in an HTML comment:
// 1-64 characters from [A-Za-z0-9._/-].
func ValidCommentID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '.' || c == '_' || c == '/' || c == '-'
		if !ok {
			return false
		}
	}
	return !strings.Contains(id, "--")
}

// Options customize the Markdown output.
type Options struct {
	// CommentID selects the hidden marker (see Marker).
	CommentID string
	// FailAbove is the monthly increase limit, if one is configured.
	FailAbove *decimal.Decimal
	// LimitSource names where the limit came from, e.g. ".tf-plan-cost.yml".
	LimitSource string
}

// Markdown writes the report as a GitHub-flavored Markdown PR comment.
func Markdown(w io.Writer, r *pricing.Report, opts Options) error {
	var b strings.Builder
	changed := r.Changed()
	var unchangedCount int
	var unchangedTotal decimal.Decimal
	for _, it := range r.Items {
		if it.Action == plan.NoOp {
			unchangedCount++
			unchangedTotal = unchangedTotal.Add(it.After.Monthly)
		}
	}
	// changedUnpriced are supported resources we could not price: their cost
	// is real but unknown, so they get a warning. Unsupported types (often
	// free, like IAM or S3 config) are only listed.
	var changedUnpriced, unchangedUnpriced []pricing.UnpricedResource
	for _, u := range r.Unpriced {
		if u.Action == plan.NoOp {
			unchangedUnpriced = append(unchangedUnpriced, u)
		} else {
			changedUnpriced = append(changedUnpriced, u)
		}
	}
	notPriced := append(append([]pricing.UnpricedResource{}, changedUnpriced...), r.Unsupported...)

	b.WriteString(Marker(opts.CommentID) + "\n")
	if !r.HasChanges() {
		fmt.Fprintf(&b, "### tf-plan-cost: no cost change\n\nEstimated total: %s/mo\n", usd(r.TotalAfter, false))
	} else {
		fmt.Fprintf(&b, "### tf-plan-cost: **%s/mo** (was %s → now %s)\n",
			usd(r.Delta, true), usd(r.TotalBefore, false), usd(r.TotalAfter, false))
	}

	if opts.FailAbove != nil {
		source := "the tf-plan-cost config"
		if opts.LimitSource != "" {
			source = "`" + opts.LimitSource + "`"
		}
		if r.Delta.GreaterThan(*opts.FailAbove) {
			fmt.Fprintf(&b, "\n> [!CAUTION]\n> This change adds **%s/mo**, which is above the limit of **%s/mo** set in %s.\n",
				usd(r.Delta, true), usd(*opts.FailAbove, false), source)
		} else {
			fmt.Fprintf(&b, "\nWithin the %s/mo limit set in %s.\n", usd(*opts.FailAbove, false), source)
		}
	}

	if len(changedUnpriced) > 0 {
		fmt.Fprintf(&b, "\n> [!WARNING]\n> %s could not be priced, so the real change may differ. See **Not priced** below.\n",
			plural(len(changedUnpriced), "changed resource", "changed resources"))
	}

	if len(changed) > 0 {
		b.WriteString("\n| Resource | Action | Before | After | Δ/mo |\n|---|---|---|---|---|\n")
		for _, it := range changed {
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s |\n",
				cell(it.Address), cell(actionText(it)), side(it.Before), side(it.After), usd(it.Delta, true))
		}
	}

	if notes := collectNotes(changed); len(notes) > 0 {
		fmt.Fprintf(&b, "\n<details><summary>Notes (%d)</summary>\n\n", len(notes))
		for _, n := range notes {
			fmt.Fprintf(&b, "- %s\n", n)
		}
		b.WriteString("\n</details>\n")
	}

	if len(notPriced) > 0 {
		fmt.Fprintf(&b, "\n<details><summary>Not priced (%d)</summary>\n\n", len(notPriced))
		b.WriteString("| Resource | Action | Reason |\n|---|---|---|\n")
		for _, u := range notPriced {
			fmt.Fprintf(&b, "| `%s` | %s | %s |\n", cell(u.Address), u.Action, cell(u.Reason))
		}
		b.WriteString("\n</details>\n")
	}

	b.WriteString("\n<sub>Estimated with on-demand Linux prices and 730 hours/month from the AWS Price List API")
	if !r.PricesAsOf.IsZero() {
		fmt.Fprintf(&b, " (prices as of %s)", r.PricesAsOf.UTC().Format("2006-01-02"))
	}
	b.WriteString(". Usage-based charges such as data transfer, requests and LCUs are not included.")
	if unchangedCount > 0 && len(changed) > 0 {
		fmt.Fprintf(&b, " %s (%s/mo) not shown.", plural(unchangedCount, "unchanged resource", "unchanged resources"), usd(unchangedTotal, false))
	}
	if len(unchangedUnpriced) > 0 {
		verb := "are"
		if len(unchangedUnpriced) == 1 {
			verb = "is"
		}
		fmt.Fprintf(&b, " %s could not be priced and %s excluded from the totals.",
			plural(len(unchangedUnpriced), "unchanged resource", "unchanged resources"), verb)
	}
	b.WriteString("</sub>\n")

	_, err := io.WriteString(w, b.String())
	return err
}

func actionText(it pricing.LineItem) string {
	before, after := summary(it.Before), summary(it.After)
	switch it.Action {
	case plan.Create:
		return "+ " + after
	case plan.Delete:
		return "- " + before
	case plan.Replace:
		return "-/+ " + transition(before, after)
	default:
		return "~ " + transition(before, after)
	}
}

func transition(before, after string) string {
	if before == after {
		return after
	}
	return before + " → " + after
}

func summary(e *pricing.Estimate) string {
	if e == nil {
		return ""
	}
	return e.Summary
}

func side(e *pricing.Estimate) string {
	if e == nil {
		return "–"
	}
	return usd(e.Monthly, false)
}

func collectNotes(items []pricing.LineItem) []string {
	var out []string
	for _, it := range items {
		e := it.After
		if e == nil {
			e = it.Before
		}
		for _, n := range e.Notes {
			out = append(out, fmt.Sprintf("`%s`: %s", it.Address, n))
		}
	}
	return out
}

// usd formats a dollar amount with thousands separators, e.g. $1,204.10.
// signed adds a + for positive amounts.
func usd(d decimal.Decimal, signed bool) string {
	s := d.Abs().StringFixed(2)
	whole, frac, _ := strings.Cut(s, ".")
	var g strings.Builder
	for i, c := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			g.WriteByte(',')
		}
		g.WriteRune(c)
	}
	out := "$" + g.String() + "." + frac
	switch {
	case d.Round(2).IsNegative():
		return "-" + out
	case signed && d.Round(2).IsPositive():
		return "+" + out
	default:
		return out
	}
}

func cell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.ReplaceAll(s, "\n", " ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// Headline is a one-line result for a check run title, e.g. "+$242.24/mo",
// or "No cost change" when no resources change.
func Headline(r *pricing.Report) string {
	if !r.HasChanges() {
		return "No cost change"
	}
	return usd(r.Delta, true) + "/mo"
}
