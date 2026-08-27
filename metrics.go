package nacos

import (
	"errors"
	"fmt"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

var metricsRegistrationMu sync.Mutex

// Metrics defines Nacos-related Prometheus metrics
type Metrics struct {
	sdkOperationsTotal    *prometheus.CounterVec
	sdkErrorsTotal        *prometheus.CounterVec
	healthCheckTotal      *prometheus.CounterVec
	healthCheckFailed     *prometheus.CounterVec
	serviceDiscoveryTotal *prometheus.CounterVec
	configOperationsTotal *prometheus.CounterVec
}

// NewNacosMetrics creates a new metrics instance. Collectors are registered
// against the default registerer once and reused on subsequent calls, so
// re-initializing the plugin (restart) does not panic on duplicate registration.
func NewNacosMetrics() *Metrics {
	metricsRegistrationMu.Lock()
	defer metricsRegistrationMu.Unlock()

	return &Metrics{
		sdkOperationsTotal: registerCounterVec(
			prometheus.CounterOpts{
				Namespace: "lynx",
				Subsystem: "nacos",
				Name:      "sdk_operations_total",
				Help:      "Total number of Nacos SDK operations",
			},
			[]string{"operation", "status"},
		),
		sdkErrorsTotal: registerCounterVec(
			prometheus.CounterOpts{
				Namespace: "lynx",
				Subsystem: "nacos",
				Name:      "sdk_errors_total",
				Help:      "Total number of Nacos SDK errors",
			},
			[]string{"operation", "error_type"},
		),
		healthCheckTotal: registerCounterVec(
			prometheus.CounterOpts{
				Namespace: "lynx",
				Subsystem: "nacos",
				Name:      "health_check_total",
				Help:      "Total number of Nacos health checks",
			},
			[]string{"component", "status"},
		),
		healthCheckFailed: registerCounterVec(
			prometheus.CounterOpts{
				Namespace: "lynx",
				Subsystem: "nacos",
				Name:      "health_check_failed_total",
				Help:      "Total number of failed Nacos health checks",
			},
			[]string{"component", "error_type"},
		),
		serviceDiscoveryTotal: registerCounterVec(
			prometheus.CounterOpts{
				Namespace: "lynx",
				Subsystem: "nacos",
				Name:      "service_discovery_total",
				Help:      "Total number of service discovery operations",
			},
			[]string{"service", "status"},
		),
		configOperationsTotal: registerCounterVec(
			prometheus.CounterOpts{
				Namespace: "lynx",
				Subsystem: "nacos",
				Name:      "config_operations_total",
				Help:      "Total number of config operations",
			},
			[]string{"operation", "data_id", "status"},
		),
	}
}

// RecordSDKOperation records an SDK operation
func (m *Metrics) RecordSDKOperation(operation, status string) {
	m.sdkOperationsTotal.WithLabelValues(operation, status).Inc()
}

// RecordSDKError records an SDK error
func (m *Metrics) RecordSDKError(operation, errorType string) {
	m.sdkErrorsTotal.WithLabelValues(operation, errorType).Inc()
}

// RecordHealthCheck records a health check
func (m *Metrics) RecordHealthCheck(component, status string) {
	m.healthCheckTotal.WithLabelValues(component, status).Inc()
}

// RecordHealthCheckFailed records a failed health check
func (m *Metrics) RecordHealthCheckFailed(component, errorType string) {
	m.healthCheckFailed.WithLabelValues(component, errorType).Inc()
}

// RecordServiceDiscovery records a service discovery operation
func (m *Metrics) RecordServiceDiscovery(service, status string) {
	m.serviceDiscoveryTotal.WithLabelValues(service, status).Inc()
}

// RecordConfigOperation records a config operation
func (m *Metrics) RecordConfigOperation(operation, dataId, status string) {
	m.configOperationsTotal.WithLabelValues(operation, dataId, status).Inc()
}

// registerCounterVec registers a CounterVec with the default registerer, or
// returns the already-registered collector when one with the same descriptor exists.
func registerCounterVec(opts prometheus.CounterOpts, labelNames []string) *prometheus.CounterVec {
	collector := prometheus.NewCounterVec(opts, labelNames)
	if err := prometheus.DefaultRegisterer.Register(collector); err != nil {
		var alreadyRegistered prometheus.AlreadyRegisteredError
		if errors.As(err, &alreadyRegistered) {
			existing, ok := alreadyRegistered.ExistingCollector.(*prometheus.CounterVec)
			if !ok {
				panic(fmt.Sprintf("unexpected counter collector type for %s_%s_%s", opts.Namespace, opts.Subsystem, opts.Name))
			}
			return existing
		}
		panic(fmt.Sprintf("failed to register counter collector %s_%s_%s: %v", opts.Namespace, opts.Subsystem, opts.Name, err))
	}
	return collector
}
