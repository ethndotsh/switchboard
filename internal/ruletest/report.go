package ruletest

import (
	"fmt"
	"io"
)

// Format writes a human-readable report. With verbose, passing cases are
// listed too; failures always show field-level diffs.
func (r Report) Format(w io.Writer, verbose bool) {
	for _, result := range r.Results {
		switch {
		case result.Passed:
			if verbose {
				fmt.Fprintf(w, "PASS  %s\n", result.Name)
			}
		case result.Errored:
			fmt.Fprintf(w, "ERROR %s\n", result.Name)
			for _, failure := range result.Failures {
				fmt.Fprintf(w, "      %s\n", failure)
			}
		default:
			fmt.Fprintf(w, "FAIL  %s\n", result.Name)
			for _, failure := range result.Failures {
				fmt.Fprintf(w, "      %s\n", failure)
			}
		}
	}
	fmt.Fprintf(w, "%d passed, %d failed, %d errored (%d total)\n", r.Passed, r.Failed, r.Errored, r.Total)
}
