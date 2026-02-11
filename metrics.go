package nacos

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics defines Nacos-related Prometheus metrics
type Metrics struct {
	sdkOperationsTotal    *prometheus.CounterVec
	sdkErrorsTotal        *prometheus.CounterVec
	healthCheckTotal      *prometheus.CounterVec
	healthCheckFailed     *prometheus.CounterVec
	serviceDiscoveryTotal *prometheus.CounterVec
	configOperationsTotal *prometheus.CounterVec
}

// NewNacosMetrics creates a new metrics instance
func NewNacosMetrics() *Metrics {
	return &Metrics{
		sdkOperationsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "lynx",
				Subsystem: "nacos",
				Name:      "sdk_operations_total",
				Help:      "Total number of Nacos SDK operations",
			},
			[]string{"operation", "status"},
		),
		sdkErrorsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "lynx",
				Subsystem: "nacos",
				Name:      "sdk_errors_total",
				Help:      "Total number of Nacos SDK errors",
			},
			[]string{"operation", "error_type"},
		),
		healthCheckTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "lynx",
				Subsystem: "nacos",
				Name:      "health_check_total",
				Help:      "Total number of Nacos health checks",
			},
			[]string{"component", "status"},
		),
		healthCheckFailed: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "lynx",
				Subsystem: "nacos",
				Name:      "health_check_failed_total",
				Help:      "Total number of failed Nacos health checks",
			},
			[]string{"component", "error_type"},
		),
		serviceDiscoveryTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "lynx",
				Subsystem: "nacos",
				Name:      "service_discovery_total",
				Help:      "Total number of service discovery operations",
			},
			[]string{"service", "status"},
		),
		configOperationsTotal: promauto.NewCounterVec(
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
