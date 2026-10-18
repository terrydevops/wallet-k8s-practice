package probe

import (
	"math/big"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics are registered on the caller's registry; there are no package-level globals.
type Metrics struct {
	probes      *prometheus.CounterVec
	duration    *prometheus.HistogramVec
	lastSuccess *prometheus.GaugeVec
	winner      *prometheus.CounterVec
	routes      *prometheus.GaugeVec
	amountOut   *prometheus.GaugeVec
	priceImpact *prometheus.GaugeVec
}

func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		probes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "swap_probe_total", Help: "Probe attempts by step and result.",
		}, []string{"aggregator", "scenario", "step", "result"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "swap_probe_duration_seconds", Help: "Probe latency by step, including the client rate-limit wait.",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 3, 5, 10},
		}, []string{"aggregator", "scenario", "step"}),
		lastSuccess: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "swap_probe_last_success_timestamp_seconds", Help: "Unix time of the last successful quote.",
		}, []string{"aggregator", "scenario"}),
		winner: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "swap_quote_winner_total", Help: "Provider of the best route, per successful quote.",
		}, []string{"aggregator", "scenario", "provider"}),
		routes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "swap_quote_routes", Help: "Routes offered in the last successful quote.",
		}, []string{"aggregator", "scenario"}),
		amountOut: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "swap_quote_amount_out", Help: "Expected output of the best route, smallest units (float, precision may be lost).",
		}, []string{"aggregator", "scenario"}),
		priceImpact: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "swap_quote_price_impact", Help: "Price impact of the best route as reported by the aggregator.",
		}, []string{"aggregator", "scenario"}),
	}
	reg.MustRegister(m.probes, m.duration, m.lastSuccess, m.winner, m.routes, m.amountOut, m.priceImpact)
	return m
}

// parseAmount converts a smallest-unit integer string to float64 for a gauge.
func parseAmount(s string) (float64, bool) {
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f, true
	}
	if n, ok := new(big.Int).SetString(s, 10); ok {
		f, _ := new(big.Float).SetInt(n).Float64()
		return f, true
	}
	return 0, false
}
