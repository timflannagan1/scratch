package prom

/*
Note(s):
- Most of the custom types and the promMatrixToPrometheusMetrics implementation
  were taken from the github.com/kube-reporting/metering-operator and adjusted
  accordingly.
- Need to eventually create a custom type for a Prometheus query runner.
- Need to handle errors better.
- Need to be able to pass a v1.Range parameter to the ExecPromQuery function
  instead of harcoding those lookback values.
*/

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/transport"
)

// PrometheusImporterConfig is holds the configuration needed to establish a
// connection to Prometheus to import metrics into Postgres.
type PrometheusImporterConfig struct {
	Hostname            string
	Address             *url.URL
	BearerToken         string
	BearerTokenFile     string
	SkipTLSVerification bool
}

// PrometheusMetric is a receipt of a usage determined by a query within a specific time range.
type PrometheusMetric struct {
	Labels    map[string]string `json:"labels"`
	Amount    float64           `json:"amount"`
	StepSize  time.Duration     `json:"stepSize"`
	Timestamp time.Time         `json:"timestamp"`
	Dt        string            `json:"dt"`
}

// NewPrometheusAPIClient is a helper function responsible for setting up an API
// client to the Prometheus instance at the @address URL.
func NewPrometheusAPIClient(logger logrus.FieldLogger, cfg PrometheusImporterConfig) (v1.API, error) {
	transportConfig := &transport.Config{
		TLS: transport.TLSConfig{
			Insecure: cfg.SkipTLSVerification,
		},
	}
	if cfg.BearerTokenFile != "" {
		transportConfig.BearerTokenFile = cfg.BearerTokenFile
	}
	if cfg.BearerToken != "" {
		transportConfig.BearerToken = cfg.BearerToken
	}

	ht, err := transport.New(transportConfig)
	if err != nil {
		return nil, fmt.Errorf("Failed to initialize a RoundTripper: %v", err)
	}

	client, err := api.NewClient(api.Config{
		Address:      cfg.Address.String(),
		RoundTripper: ht,
	})
	if err != nil {
		return nil, fmt.Errorf("Failed to create the Prometheus API client: %+v", err)
	}

	return v1.NewAPI(client), nil
}

// ExecPromQuery is responsible for firing off a promQL query to the query_range
// Prometheus API endpoint and returning an initialized list of the PrometheusMetric
// type based on the matrix the promQL had returned.
func ExecPromQuery(ctx context.Context, logger logrus.FieldLogger, apiClient v1.API, query string) ([]*PrometheusMetric, error) {
	r := v1.Range{
		Start: time.Now().Add(-5 * time.Minute),
		End:   time.Now(),
		Step:  time.Minute,
	}
	res, err := apiClient.QueryRange(ctx, query, r)
	if err != nil {
		return nil, err
	}
	matrix, ok := res.(model.Matrix)
	if !ok {
		return nil, fmt.Errorf("Failed to safely index the model matrix: %+v", err)
	}
	logger.Infof("Finished executing the %s query", query)

	return promMatrixToPrometheusMetrics(r, matrix), nil
}

// promMatrixToPrometheusMetrics is a helper function responsible for building
// up a PrometheusMetric structure based on the @matrix model.Matrix.
func promMatrixToPrometheusMetrics(timeRange v1.Range, matrix model.Matrix) []*PrometheusMetric {
	var metrics []*PrometheusMetric
	for _, ss := range matrix {
		labels := make(map[string]string, len(ss.Metric))
		for k, v := range ss.Metric {
			labels[string(k)] = string(v)
		}
		for _, value := range ss.Values {
			metrics = append(metrics, &PrometheusMetric{
				Labels:    labels,
				Amount:    float64(value.Value),
				StepSize:  timeRange.Step,
				Timestamp: value.Timestamp.Time().UTC(),
			})
		}
	}
	return metrics
}
