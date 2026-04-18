package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	PoolSize = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "proxy_pool_size",
		Help: "Current pool size by state.",
	}, []string{"state"})

	PoolBySource = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "proxy_pool_source_size",
		Help: "Current active pool size by source plugin.",
	}, []string{"source"})

	RequestTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "proxy_request_total",
		Help: "Total proxy requests handled.",
	}, []string{"result"}) // success | fail | retry | rejected | direct

	RequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "proxy_request_duration_seconds",
		Help:    "End-to-end request duration handled by the proxy.",
		Buckets: prometheus.ExponentialBuckets(0.05, 2, 10),
	}, []string{"result"})

	UpstreamTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "proxy_upstream_total",
		Help: "Upstream SOCKS5 attempts.",
	}, []string{"result"}) // success | fail

	VerifyTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "proxy_verify_total",
		Help: "Proxy validation attempts by source.",
	}, []string{"source", "result"}) // pass | fail
)

func init() {
	prometheus.MustRegister(PoolSize, PoolBySource, RequestTotal, RequestDuration, UpstreamTotal, VerifyTotal)
}

func Handler() http.Handler { return promhttp.Handler() }
