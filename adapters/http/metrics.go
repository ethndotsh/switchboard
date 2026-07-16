package httpadapter

import (
	"time"

	"github.com/ethndotsh/switchboard"
	"github.com/ethndotsh/switchboard/engine"
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics labels stay low-cardinality by design: decision has five values,
// result has two.
type Metrics struct {
	invocations *prometheus.CounterVec
	duration    prometheus.Histogram
}

func NewMetrics(reg prometheus.Registerer, service *engine.Service) (*Metrics, error) {
	m := &Metrics{
		invocations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "switchboard_invocations_total",
			Help: "Rule invocations by decision and result.",
		}, []string{"decision", "result"}),
		duration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "switchboard_invocation_duration_seconds",
			Help:    "Rule invocation latency.",
			Buckets: []float64{.00005, .0001, .00025, .0005, .001, .0025, .005, .01, .025, .05, .1},
		}),
	}
	collectors := []prometheus.Collector{m.invocations, m.duration}
	if service != nil {
		collectors = append(collectors,
			prometheus.NewGaugeFunc(prometheus.GaugeOpts{
				Name: "switchboard_pool_instances",
				Help: "Warm pool instances for the active runtime.",
			}, func() float64 { return float64(service.Status().Pool.TotalInstances) }),
			prometheus.NewGaugeFunc(prometheus.GaugeOpts{
				Name: "switchboard_pool_inflight",
				Help: "In-flight invocations on the active runtime.",
			}, func() float64 { return float64(service.Status().Pool.Inflight) }),
			prometheus.NewCounterFunc(prometheus.CounterOpts{
				Name: "switchboard_pool_exhaustions_total",
				Help: "Requests that found the warm pool empty.",
			}, func() float64 { return float64(service.Status().Pool.Exhaustions) }),
			prometheus.NewCounterFunc(prometheus.CounterOpts{
				Name: "switchboard_activation_total",
				Help: "Bundle activations by result.",
				ConstLabels: prometheus.Labels{
					"result": "success",
				},
			}, func() float64 { return float64(service.State().ActivationsSucceeded) }),
			prometheus.NewCounterFunc(prometheus.CounterOpts{
				Name: "switchboard_activation_total",
				Help: "Bundle activations by result.",
				ConstLabels: prometheus.Labels{
					"result": "failure",
				},
			}, func() float64 { return float64(service.State().ActivationsFailed) }),
		)
	}
	for _, collector := range collectors {
		if err := reg.Register(collector); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (m *Metrics) ObserveInvocation(decision switchboard.Decision, err error, elapsed time.Duration) {
	if m == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "error"
		decision = ""
	}
	label := string(decision)
	if label == "" {
		label = "none"
	}
	m.invocations.WithLabelValues(label, result).Inc()
	m.duration.Observe(elapsed.Seconds())
}
