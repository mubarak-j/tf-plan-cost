package render

import (
	"encoding/json"
	"io"

	"github.com/mubarak-j/tf-plan-cost/internal/pricing"
)

// JSON writes the report as indented JSON. Amounts are decimal strings to
// avoid floating-point rounding.
func JSON(w io.Writer, r *pricing.Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}
