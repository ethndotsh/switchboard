package engine

import (
	"context"
	"fmt"

	"github.com/ethndotsh/switchboard/internal/bundle"
	"github.com/ethndotsh/switchboard/internal/ruletest"
	"go.uber.org/zap"
)

// runTestSuite runs a bundle's embedded tests against the warmed candidate
// before activation; a failing suite quarantines the candidate.
func (r *Reconciler) runTestSuite(ctx context.Context, candidate *Runtime, b bundle.Bundle) error {
	suite, err := ruletest.ParseSuite(b.Tests)
	if err != nil {
		return fmt.Errorf("%w: embedded tests: %v", bundle.ErrInvalid, err)
	}
	report := suite.Run(ctx, candidate)
	r.updateState(func(state *ReconcilerState) {
		state.LastTestReport = report.Summary()
	})
	if !report.OK() {
		for _, result := range report.Results {
			if result.Passed {
				continue
			}
			r.log().Warn("switchboard embedded test case failed",
				zap.String("bundle_id", b.ID),
				zap.String("case", result.Name),
				zap.Strings("failures", result.Failures))
		}
		return fmt.Errorf("embedded test suite failed: %s", report.Summary())
	}
	r.log().Info("switchboard embedded test suite passed", zap.String("bundle_id", b.ID), zap.String("report", report.Summary()))
	return nil
}
