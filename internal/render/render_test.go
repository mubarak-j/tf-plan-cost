package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/mubarak-j/tf-plan-cost/internal/plan"
	"github.com/mubarak-j/tf-plan-cost/internal/pricing"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func est(summary, monthly string, notes ...string) *pricing.Estimate {
	return &pricing.Estimate{Summary: summary, Monthly: d(monthly), Notes: notes}
}

func markdown(t *testing.T, r *pricing.Report) string {
	t.Helper()
	var b bytes.Buffer
	if err := Markdown(&b, r, Options{}); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestUSD(t *testing.T) {
	tests := []struct {
		in     string
		signed bool
		want   string
	}{
		{"0", false, "$0.00"},
		{"0", true, "$0.00"},
		{"0.004", true, "$0.00"},
		{"0.005", true, "+$0.01"},
		{"16.425", false, "$16.43"},
		{"412.8", true, "+$412.80"},
		{"-122.28", true, "-$122.28"},
		{"-122.28", false, "-$122.28"},
		{"1204.1", false, "$1,204.10"},
		{"1234567.891", true, "+$1,234,567.89"},
		{"100", false, "$100.00"},
		{"999.999", false, "$1,000.00"},
	}
	for _, tt := range tests {
		if got := usd(d(tt.in), tt.signed); got != tt.want {
			t.Errorf("usd(%s, %v) = %s, want %s", tt.in, tt.signed, got, tt.want)
		}
	}
}

func TestMarkdownChanges(t *testing.T) {
	r := &pricing.Report{
		Items: []pricing.LineItem{
			{Address: "aws_instance.api[0]", Action: plan.Update, Before: est("m6i.large", "70.08"), After: est("m6i.xlarge", "140.16"), Delta: d("70.08")},
			{Address: "aws_ebs_volume.data", Action: plan.Create, After: est("gp3 500 GB", "40", "a note"), Delta: d("40")},
			{Address: `module.x["a|b"].aws_lb.this`, Action: plan.Delete, Before: est("Network Load Balancer", "16.425"), Delta: d("-16.425")},
			{Address: "aws_db_instance.db", Action: plan.Replace, Before: est("db.t4g.micro", "10"), After: est("db.t4g.micro", "10"), Delta: d("0")},
			{Address: "aws_nat_gateway.main", Action: plan.NoOp, Before: est("NAT", "32.85"), After: est("NAT", "32.85")},
		},
		Unpriced: []pricing.UnpricedResource{
			{Address: "aws_instance.spot", Action: plan.Create, Reason: "spot instances are not priced"},
			{Address: "aws_instance.old", Action: plan.NoOp, Reason: "x"},
		},
		Unsupported: []pricing.UnpricedResource{{Address: "aws_s3_bucket.logs", Action: plan.Update, Reason: "resource type not supported yet"}},
		TotalBefore: d("1204.1"), TotalAfter: d("1616.9"), Delta: d("412.8"),
		PricesAsOf: time.Date(2026, 11, 6, 23, 0, 0, 0, time.FixedZone("MST", -7*3600)),
	}
	got := markdown(t, r)
	for _, want := range []string{
		Marker("") + "\n### tf-plan-cost: **+$412.80/mo** (was $1,204.10 → now $1,616.90)\n",
		"> [!WARNING]\n> 1 changed resource could not be priced",
		"| `aws_instance.api[0]` | ~ m6i.large → m6i.xlarge | $70.08 | $140.16 | +$70.08 |",
		"| `aws_ebs_volume.data` | + gp3 500 GB | – | $40.00 | +$40.00 |",
		"| `module.x[\"a\\|b\"].aws_lb.this` | - Network Load Balancer | $16.43 | – | -$16.43 |",
		"| `aws_db_instance.db` | -/+ db.t4g.micro | $10.00 | $10.00 | $0.00 |",
		"- `aws_ebs_volume.data`: a note",
		"<summary>Not priced (2)</summary>",
		"| `aws_instance.spot` | create | spot instances are not priced |",
		"| `aws_s3_bucket.logs` | update | resource type not supported yet |",
		"(prices as of 2026-11-07)", // UTC date
		"1 unchanged resource ($32.85/mo) not shown.",
		"1 unchanged resource could not be priced and is excluded from the totals.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "aws_nat_gateway.main") {
		t.Error("unchanged resources must not be listed")
	}
}

func TestMarkdownUnsupportedOnlyHasNoWarning(t *testing.T) {
	got := markdown(t, &pricing.Report{
		Unsupported: []pricing.UnpricedResource{{Address: "aws_iam_role.x", Action: plan.Create, Reason: "resource type not supported yet"}},
	})
	if strings.Contains(got, "[!WARNING]") {
		t.Errorf("unsupported-only report should not warn:\n%s", got)
	}
	if !strings.Contains(got, "**$0.00/mo**") || !strings.Contains(got, "Not priced (1)") {
		t.Errorf("got:\n%s", got)
	}
}

func TestMarkdownNoChange(t *testing.T) {
	got := markdown(t, &pricing.Report{
		Items:      []pricing.LineItem{{Address: "a", Action: plan.NoOp, Before: est("x", "5"), After: est("x", "5")}},
		TotalAfter: d("5"), TotalBefore: d("5"),
	})
	if !strings.Contains(got, "### tf-plan-cost: no cost change\n\nEstimated total: $5.00/mo") {
		t.Errorf("got:\n%s", got)
	}
	if strings.Contains(got, "| Resource |") || strings.Contains(got, "not shown") || strings.Contains(got, "prices as of") {
		t.Errorf("no-change comment should have no table, hidden count or date:\n%s", got)
	}
}

func TestJSON(t *testing.T) {
	r := &pricing.Report{
		Items: []pricing.LineItem{{Address: "a", Type: "aws_instance", Action: plan.Create, After: est("m6i.large", "70.08"), Delta: d("70.08")}},
		Delta: d("70.08"), TotalAfter: d("70.08"),
	}
	var b bytes.Buffer
	if err := JSON(&b, r); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, b.String())
	}
	if got["delta_monthly_usd"] != "70.08" {
		t.Errorf("delta_monthly_usd = %#v, want decimal string", got["delta_monthly_usd"])
	}
	item := got["items"].([]any)[0].(map[string]any)
	if item["action"] != "create" || item["before"] != nil {
		t.Errorf("item = %v", item)
	}
}

func TestMarker(t *testing.T) {
	tests := map[string]string{
		"":             "<!-- tf-plan-cost -->",
		"default":      "<!-- tf-plan-cost -->",
		"prod":         "<!-- tf-plan-cost:prod -->",
		"envs/staging": "<!-- tf-plan-cost:envs/staging -->",
	}
	for id, want := range tests {
		if got := Marker(id); got != want {
			t.Errorf("Marker(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestValidCommentID(t *testing.T) {
	for _, id := range []string{"default", "prod", "envs/us-east-1", "a.b_c"} {
		if !ValidCommentID(id) {
			t.Errorf("ValidCommentID(%q) = false, want true", id)
		}
	}
	for _, id := range []string{"", "has space", "x-->", "a--b", "<b>", strings.Repeat("a", 65)} {
		if ValidCommentID(id) {
			t.Errorf("ValidCommentID(%q) = true, want false", id)
		}
	}
}

func TestMarkdownLimit(t *testing.T) {
	limit := d("500")
	r := &pricing.Report{
		Items: []pricing.LineItem{{Address: "a", Action: plan.Create, After: est("x", "600"), Delta: d("600")}},
		Delta: d("600"), TotalAfter: d("600"),
	}
	var b bytes.Buffer
	if err := Markdown(&b, r, Options{CommentID: "prod", FailAbove: &limit, LimitSource: ".tf-plan-cost.yml"}); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	for _, want := range []string{
		"<!-- tf-plan-cost:prod -->\n",
		"> [!CAUTION]\n> This change adds **+$600.00/mo**, which is above the limit of **$500.00/mo** set in `.tf-plan-cost.yml`.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}

	r.Delta = d("-50")
	b.Reset()
	if err := Markdown(&b, r, Options{FailAbove: &limit}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "Within the $500.00/mo limit set in the tf-plan-cost config.") {
		t.Errorf("got:\n%s", b.String())
	}
}

func TestHeadline(t *testing.T) {
	changed := &pricing.Report{
		Items: []pricing.LineItem{{Address: "aws_instance.a", Action: plan.Update, Delta: d("1242.235")}},
		Delta: d("1242.235"),
	}
	if got := Headline(changed); got != "+$1,242.24/mo" {
		t.Errorf("Headline(changed) = %q", got)
	}
	decrease := &pricing.Report{
		Items: []pricing.LineItem{{Address: "aws_instance.a", Action: plan.Delete, Delta: d("-5")}},
		Delta: d("-5"),
	}
	if got := Headline(decrease); got != "-$5.00/mo" {
		t.Errorf("Headline(decrease) = %q", got)
	}
	if got := Headline(&pricing.Report{}); got != "No cost change" {
		t.Errorf("Headline(empty) = %q", got)
	}
}
