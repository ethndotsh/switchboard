package httpadapter

import (
	"errors"
	"testing"
	"time"

	"github.com/ethndotsh/switchboard"
	"github.com/prometheus/client_golang/prometheus"
)

func BenchmarkObserveInvocation(b *testing.B) {
	metrics, err := NewMetrics(prometheus.NewRegistry(), nil)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		metrics.ObserveInvocation(switchboard.DecisionNext, nil, 5*time.Microsecond)
	}
}

func BenchmarkObserveInvocationError(b *testing.B) {
	metrics, err := NewMetrics(prometheus.NewRegistry(), nil)
	if err != nil {
		b.Fatal(err)
	}
	failure := errors.New("boom")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		metrics.ObserveInvocation(switchboard.DecisionDeny, failure, 5*time.Microsecond)
	}
}
