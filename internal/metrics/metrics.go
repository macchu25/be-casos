package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	InferenceResults = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cas_inference_results_total",
		Help: "Total number of AI inference results processed",
	}, []string{"label"})

	ActiveAlerts = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "cas_active_alerts_total",
		Help: "Current number of active fall alerts",
	})

	EmergencyCalls = promauto.NewCounter(prometheus.CounterOpts{
		Name: "cas_emergency_calls_total",
		Help: "Total number of emergency calls triggered",
	})
)
