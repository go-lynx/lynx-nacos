package nacos

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/selector"
	"github.com/go-lynx/lynx"
	"github.com/go-lynx/lynx-nacos/conf"
	"github.com/go-lynx/lynx/log"
	"github.com/go-lynx/lynx/plugins"
	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/config_client"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/naming_client"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
)

// Plugin metadata
const (
	pluginName        = "nacos.control.plane"
	pluginVersion     = "v2.0.0"
	pluginDescription = "nacos control plane plugin for lynx framework"
	confPrefix        = "lynx.nacos"
)

// PlugNacos represents a Nacos control plane plugin instance
type PlugNacos struct {
	*plugins.BasePlugin
	conf *conf.Nacos

	// SDK clients
	namingClient naming_client.INamingClient
	configClient config_client.IConfigClient

	// Resilience components
	metrics        *Metrics
	retryManager   *RetryManager
	circuitBreaker *CircuitBreaker

	// State management
	mu          sync.RWMutex
	initialized int32
	destroyed   int32

	// Service information
	serviceInfo *ServiceInfo

	// Configuration watchers
	configWatchers map[string]*ConfigWatcher
	watcherMutex   sync.RWMutex

	// Cache system
	serviceCache map[string]interface{}
	configCache  map[string]interface{}
	cacheMutex   sync.RWMutex
}

// ServiceInfo service registration information
type ServiceInfo struct {
	Service   string
	Namespace string
	Group     string
	Cluster   string
	Host      string
	Port      int
	Metadata  map[string]string
}

// NewNacosControlPlane creates a new Nacos control plane plugin instance
func NewNacosControlPlane() *PlugNacos {
	return &PlugNacos{
		BasePlugin: plugins.NewBasePlugin(
			plugins.GeneratePluginID("", pluginName, pluginVersion),
			pluginName,
			pluginDescription,
			pluginVersion,
			confPrefix,
			math.MaxInt, // High priority
		),
		configWatchers: make(map[string]*ConfigWatcher),
		serviceCache:   make(map[string]interface{}),
		configCache:    make(map[string]interface{}),
	}
}

// InitializeResources implements custom initialization logic for the Nacos plugin
func (p *PlugNacos) InitializeResources(rt plugins.Runtime) error {
	// Initialize an empty configuration structure
	p.conf = &conf.Nacos{}

	// Scan and load Nacos configuration from runtime configuration
	err := rt.GetConfig().Value(confPrefix).Scan(p.conf)
	if err != nil {
		return WrapInitError(err, "failed to scan nacos configuration")
	}

	// Set default configuration
	p.setDefaultConfig()

	// Validate configuration
	if err := p.validateConfig(); err != nil {
		return WrapInitError(err, "configuration validation failed")
	}

	// Initialize resilience components (metrics, retry, circuit breaker)
	if err := p.initComponents(); err != nil {
		return WrapInitError(err, "failed to initialize components")
	}

	// Initialize SDK clients
	if err := p.initSDKClients(); err != nil {
		return WrapInitError(err, "failed to initialize SDK clients")
	}

	// Mark as initialized
	atomic.StoreInt32(&p.initialized, 1)

	log.Infof("Nacos plugin initialized successfully - Server: %s, Namespace: %s",
		p.conf.ServerAddresses, p.getNamespace())

	return nil
}

// initComponents initializes resilience components (metrics, retry, circuit breaker)
func (p *PlugNacos) initComponents() error {
	p.metrics = NewNacosMetrics()
	p.retryManager = NewRetryManager(3, conf.DefaultRetryInterval)

	p.circuitBreaker = NewCircuitBreaker(conf.DefaultCircuitBreakerThreshold, conf.DefaultCircuitBreakerHalfOpenTimeout)

	return nil
}

// initSDKClients initializes Nacos SDK clients
func (p *PlugNacos) initSDKClients() error {
	// Build server configs
	serverConfigs := p.buildServerConfigs()

	// Build client config
	clientConfig := p.buildClientConfig()

	// Initialize naming client if service registration or discovery is enabled
	if p.conf.EnableRegister || p.conf.EnableDiscovery {
		namingClient, err := clients.NewNamingClient(
			vo.NacosClientParam{
				ClientConfig:  clientConfig,
				ServerConfigs: serverConfigs,
			},
		)
		if err != nil {
			return fmt.Errorf("failed to create naming client: %w", err)
		}
		p.namingClient = namingClient
		log.Infof("Nacos naming client initialized")
	}

	// Initialize config client if configuration management is enabled
	if p.conf.EnableConfig {
		configClient, err := clients.NewConfigClient(
			vo.NacosClientParam{
				ClientConfig:  clientConfig,
				ServerConfigs: serverConfigs,
			},
		)
		if err != nil {
			return fmt.Errorf("failed to create config client: %w", err)
		}
		p.configClient = configClient
		log.Infof("Nacos config client initialized")
	}

	return nil
}

// buildServerConfigs builds server configurations
func (p *PlugNacos) buildServerConfigs() []constant.ServerConfig {
	// When Endpoint is set and ServerAddresses empty, SDK discovers via clientConfig.Endpoint
	addresses := normalizeServerAddresses(p.conf.ServerAddresses)
	if len(addresses) == 0 {
		addresses = []string{"127.0.0.1:8848"} // fallback for SDK compatibility
	}

	var serverConfigs []constant.ServerConfig
	for _, addr := range addresses {
		serverConfig := constant.ServerConfig{
			IpAddr:      addr,
			Port:        8848, // Default Nacos port
			ContextPath: p.conf.ContextPath,
		}

		// Parse address if it contains port
		if host, port, err := parseAddress(addr); err == nil {
			serverConfig.IpAddr = host
			serverConfig.Port = port
		}

		serverConfigs = append(serverConfigs, serverConfig)
	}

	return serverConfigs
}

// buildClientConfig builds client configuration
func (p *PlugNacos) buildClientConfig() *constant.ClientConfig {
	clientConfig := constant.NewClientConfig(
		constant.WithNamespaceId(p.getNamespaceID()),
		constant.WithTimeoutMs(uint64(p.conf.Timeout*1000)),
		constant.WithNotLoadCacheAtStart(true),
		constant.WithLogDir(p.conf.LogDir),
		constant.WithCacheDir(p.conf.CacheDir),
		constant.WithLogLevel(p.conf.LogLevel),
	)

	// Set authentication if provided
	if p.conf.Username != "" && p.conf.Password != "" {
		clientConfig.Username = p.conf.Username
		clientConfig.Password = p.conf.Password
	} else if p.conf.AccessKey != "" && p.conf.SecretKey != "" {
		clientConfig.AccessKey = p.conf.AccessKey
		clientConfig.SecretKey = p.conf.SecretKey
	}

	// Set endpoint if provided
	if p.conf.Endpoint != "" {
		clientConfig.Endpoint = p.conf.Endpoint
	}

	// Set region ID if provided
	if p.conf.RegionId != "" {
		clientConfig.RegionId = p.conf.RegionId
	}

	return clientConfig
}

// getNamespaceID returns namespace ID
func (p *PlugNacos) getNamespaceID() string {
	if p.conf.NamespaceId != "" {
		return p.conf.NamespaceId
	}
	// If namespace name is provided, we need to get namespace ID
	// For now, return namespace name as ID (Nacos supports both)
	return p.conf.Namespace
}

// getNamespace returns namespace name
func (p *PlugNacos) getNamespace() string {
	if p.conf.Namespace != "" {
		return p.conf.Namespace
	}
	if p.conf.NamespaceId != "" {
		return p.conf.NamespaceId
	}
	return conf.DefaultNamespace
}

// parseAddress parses address string to host and port
func parseAddress(addr string) (string, uint64, error) {
	// Simple parsing, can be enhanced
	parts := splitAddress(addr)
	if len(parts) != 2 {
		return addr, 8848, nil // Default port
	}

	host := parts[0]
	portStr := parts[1]

	var port uint64
	fmt.Sscanf(portStr, "%d", &port)
	if port == 0 {
		port = 8848
	}

	return host, port, nil
}

// splitAddress splits address by colon
func splitAddress(addr string) []string {
	// Handle IPv6 addresses
	if strings.Contains(addr, "[") {
		// IPv6 format: [::1]:8848
		idx := strings.LastIndex(addr, ":")
		if idx > 0 {
			return []string{addr[:idx], addr[idx+1:]}
		}
		return []string{addr}
	}

	// IPv4 format: 127.0.0.1:8848
	return strings.SplitN(addr, ":", 2)
}

// checkInitialized checks if plugin is initialized
func (p *PlugNacos) checkInitialized() error {
	if atomic.LoadInt32(&p.initialized) == 0 {
		return ErrNotInitialized
	}
	if atomic.LoadInt32(&p.destroyed) == 1 {
		return fmt.Errorf("nacos plugin has been destroyed")
	}
	return nil
}

// StartupTasks implements plugin startup interface
func (p *PlugNacos) StartupTasks() error {
	if err := p.checkInitialized(); err != nil {
		return err
	}

	// Record startup metrics
	if p.metrics != nil {
		p.metrics.RecordSDKOperation("startup", "start")
		defer func() {
			if p.metrics != nil {
				p.metrics.RecordSDKOperation("startup", "success")
			}
		}()
	}

	// Set Nacos as the Lynx control plane
	if err := lynx.Lynx().SetControlPlane(p); err != nil {
		log.Errorf("Failed to set Nacos as control plane: %v", err)
		if p.metrics != nil {
			p.metrics.RecordSDKOperation("startup", "error")
		}
		return WrapInitError(err, "failed to set control plane")
	}

	log.Infof("Nacos plugin started successfully and set as control plane")
	return nil
}

// CheckHealth implements plugin health check interface
func (p *PlugNacos) CheckHealth() error {
	if err := p.checkInitialized(); err != nil {
		return err
	}

	if p.metrics != nil {
		p.metrics.RecordHealthCheck("nacos", "start")
	}

	if p.circuitBreaker == nil || p.retryManager == nil {
		return WrapInitError(fmt.Errorf("resilience components not initialized"), "health check")
	}

	var healthErr error
	err := p.circuitBreaker.Do(func() error {
		return p.retryManager.DoWithRetry(func() error {
			if err := p.checkNacosConnectivity(); err != nil {
				healthErr = err
				return err
			}
			return nil
		})
	})

	if err != nil {
		log.Errorf("Nacos health check failed: %v", healthErr)
		if p.metrics != nil {
			p.metrics.RecordHealthCheck("nacos", "error")
			p.metrics.RecordHealthCheckFailed("nacos", fmt.Sprintf("%T", healthErr))
		}
		return WrapOperationError(healthErr, "health check")
	}

	if p.metrics != nil {
		p.metrics.RecordHealthCheck("nacos", "success")
	}
	return nil
}

// checkNacosConnectivity verifies connectivity to Nacos server
func (p *PlugNacos) checkNacosConnectivity() error {
	if p.namingClient == nil && p.configClient == nil {
		return fmt.Errorf("no Nacos client initialized (enable_register, enable_discovery, or enable_config required)")
	}

	if p.namingClient != nil {
		// Test naming client with SelectInstances (service not found = connection OK)
		param := vo.SelectInstancesParam{
			ServiceName: "lynx-nacos-health-probe",
			GroupName:   conf.DefaultGroup,
		}
		_, err := p.namingClient.SelectInstances(param)
		if err != nil && !strings.Contains(err.Error(), "not found") && !strings.Contains(err.Error(), "no instance") && !strings.Contains(err.Error(), "404") {
			return fmt.Errorf("naming client connectivity check failed: %w", err)
		}
	}

	if p.configClient != nil {
		// Test config client with a non-existent config (config not found = connection OK)
		param := vo.ConfigParam{
			DataId: "lynx-nacos-health-probe",
			Group:  conf.DefaultGroup,
		}
		_, err := p.configClient.GetConfig(param)
		if err != nil && !strings.Contains(err.Error(), "config not found") && !strings.Contains(err.Error(), "404") {
			return fmt.Errorf("config client connectivity check failed: %w", err)
		}
	}

	return nil
}

// --- ControlPlane interface implementation ---

// GetNamespace returns the Nacos namespace
func (p *PlugNacos) GetNamespace() string {
	return p.getNamespace()
}

// HTTPRateLimit returns HTTP rate limit middleware (Nacos does not have built-in rate limiting)
func (p *PlugNacos) HTTPRateLimit() middleware.Middleware {
	return nil
}

// GRPCRateLimit returns gRPC rate limit middleware (Nacos does not have built-in rate limiting)
func (p *PlugNacos) GRPCRateLimit() middleware.Middleware {
	return nil
}

// NewNodeRouter creates a node filter for service routing (returns nil, use discovery for instance selection)
func (p *PlugNacos) NewNodeRouter(serviceName string) selector.NodeFilter {
	return nil
}

// CleanupTasks implements plugin cleanup interface
func (p *PlugNacos) CleanupTasks() error {
	atomic.StoreInt32(&p.destroyed, 1)

	// Stop all config watchers (with panic recovery)
	p.watcherMutex.Lock()
	for _, watcher := range p.configWatchers {
		if watcher != nil {
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Warnf("Recovered from panic while stopping config watcher: %v", r)
					}
				}()
				watcher.Stop()
			}()
		}
	}
	p.configWatchers = make(map[string]*ConfigWatcher)
	p.watcherMutex.Unlock()

	// Clear caches
	p.cacheMutex.Lock()
	p.serviceCache = make(map[string]interface{})
	p.configCache = make(map[string]interface{})
	p.cacheMutex.Unlock()

	log.Infof("Nacos plugin cleaned up successfully")
	return nil
}

// Configure updates the plugin configuration
func (p *PlugNacos) Configure(c any) error {
	if c == nil {
		return fmt.Errorf("configuration cannot be nil")
	}

	nacosConf, ok := c.(*conf.Nacos)
	if !ok {
		return fmt.Errorf("invalid configuration type: expected *conf.Nacos, got %T", c)
	}

	// Save old configuration for rollback
	oldConf := p.conf
	p.conf = nacosConf

	// Validate new configuration
	p.setDefaultConfig()
	if err := p.validateConfig(); err != nil {
		// Rollback
		p.conf = oldConf
		return fmt.Errorf("nacos configuration validation failed: %w", err)
	}

	log.Infof("Nacos configuration updated successfully")
	return nil
}
