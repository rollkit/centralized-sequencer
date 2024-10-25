package sequencing

import (
	"github.com/go-kit/kit/metrics"
	"github.com/go-kit/kit/metrics/discard"
	"github.com/go-kit/kit/metrics/prometheus"
	stdprometheus "github.com/prometheus/client_golang/prometheus"
)

const (
	// MetricsSubsystem is a subsystem shared by all metrics exposed by this
	// package.
	MetricsSubsystem = "sequencer"
)

// MetricsProvider returns sequencing Metrics.
type MetricsProvider func(chainID string) *Metrics

// DefaultMetricsProvider returns Metrics build using Prometheus client library
// if Prometheus is enabled. Otherwise, it returns no-op Metrics.
func DefaultMetricsProvider(enabled bool) MetricsProvider {
	return func(chainID string) *Metrics {
		if enabled {
			return PrometheusMetrics("chain_id", chainID)
		}
		return NopMetrics()
	}
}

// Metrics contains metrics exposed by this package.
type Metrics struct {
	// GasPrice
	GasPrice metrics.Gauge
	// Last submitted blob size
	LastBlobSize metrics.Gauge
	// TODO(tuxcanfly): needs gas used, wallet balance from go-da
	// cost / byte
	// CostPerByte metrics.Gauge
	// Wallet Balance
	// WalletBalance metrics.Gauge
	// Transaction Status
	TransactionStatus metrics.Histogram
	// Number of pending blocks.
	NumPendingBlocks metrics.Gauge
	// Last included block height
	IncludedBlockHeight metrics.Gauge
}

// PrometheusMetrics returns Metrics build using Prometheus client library.
// Optionally, labels can be provided along with their values ("foo",
// "fooValue").
func PrometheusMetrics(labelsAndValues ...string) *Metrics {
	if len(labelsAndValues)%2 != 0 {
		panic("uneven number of labels and values; labels and values should be provided in pairs")
	}
	labels := []string{}
	for i := 0; i < len(labelsAndValues); i += 2 {
		labels = append(labels, labelsAndValues[i])
	}
	return &Metrics{
		GasPrice: prometheus.NewGaugeFrom(stdprometheus.GaugeOpts{
			Subsystem: MetricsSubsystem,
			Name:      "gas_price",
			Help:      "The gas price of DA.",
		}, labels).With(labelsAndValues...),
		LastBlobSize: prometheus.NewGaugeFrom(stdprometheus.GaugeOpts{
			Subsystem: MetricsSubsystem,
			Name:      "last_blob_size",
			Help:      "The size in bytes of the last DA blob.",
		}, labels).With(labelsAndValues...),
		TransactionStatus: prometheus.NewHistogramFrom(stdprometheus.HistogramOpts{
			Subsystem: MetricsSubsystem,
			Name:      "transaction_status",
			Help:      "Distribution of transaction statuses for DA submissions.",
			Buckets:   []float64{0, 1, 2, 3, 4, 5, 6, 7},
		}, labels).With(labelsAndValues...),
		NumPendingBlocks: prometheus.NewGaugeFrom(stdprometheus.GaugeOpts{
			Subsystem: MetricsSubsystem,
			Name:      "num_pending_blocks",
			Help:      "The number of pending blocks for DA submission.",
		}, labels).With(labelsAndValues...),
		IncludedBlockHeight: prometheus.NewGaugeFrom(stdprometheus.GaugeOpts{
			Subsystem: MetricsSubsystem,
			Name:      "included_block_height",
			Help:      "The last DA included block height.",
		}, labels).With(labelsAndValues...),
	}
}

// NopMetrics returns no-op Metrics.
func NopMetrics() *Metrics {
	return &Metrics{
		GasPrice:            discard.NewGauge(),
		LastBlobSize:        discard.NewGauge(),
		TransactionStatus:   discard.NewHistogram(),
		NumPendingBlocks:    discard.NewGauge(),
		IncludedBlockHeight: discard.NewGauge(),
	}
}
