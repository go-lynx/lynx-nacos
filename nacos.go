// Package nacos provides a Nacos configuration-centre plugin for the Lynx framework.
// It connects to a Nacos server, watches one or more configuration data-IDs for live
// changes, and propagates updates to the Lynx runtime configuration layer.
// Prometheus metrics track config-fetch latency, watch events, and error rates.
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

const (
	pluginName        = "nacos.control.plane"
	pluginVersion     = "v1.6.3"
	pluginDescription = "nacos control plane plugin for lynx framework"
	// confPrefix is the config key prefix under which this plugin's settings are read.
	confPrefix = "lynx.nacos"
)

// PlugNacos is the Nacos control plane plugin. It can act as a config center, a
// service registry, and a discovery client depending on which features are
// enabled in the config.
type PlugNacos struct {
	*plugins.BasePlugin
	conf *conf.Nacos
	rt   plugins.Runtime

	// namingClient backs registry/discovery; configClient backs config. Either
	// may be nil when the corresponding feature is disabled.
	namingClient naming_client.INamingClient
	configClient config_client.IConfigClient

	metrics        *Metrics
	retryManager   *RetryManager
	circuitBreaker *CircuitBreaker

	// initialized/destroyed are atomic so checkInitialized can be called from
	// any goroutine without holding mu.
	mu          sync.RWMutex
	initialized int32
	destroyed   int32

	serviceInfo *ServiceInfo

	// configWatchers is keyed by "dataId:group"; guarded by watcherMutex.
	configWatchers map[string]*ConfigWatcher
	watcherMutex   sync.RWMutex

	// serviceCache/configCache are guarded by cacheMutex.
	serviceCache map[string]any
	configCache  map[string]any
	cacheMutex   sync.RWMutex
}

// ServiceInfo holds the identity used when registering this service instance.
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
			// Max weight: the control plane must load before plugins that
			// depend on it for config/discovery.
			math.MaxInt,
		),
		configWatchers: make(map[string]*ConfigWatcher),
		serviceCache:   make(map[string]any),
		configCache:    make(map[string]any),
	}
}

// InitializeResources scans the "lynx.nacos" config subtree, applies defaults,
// validates it, and connects the Nacos SDK clients.
func (p *PlugNacos) InitializeResources(rt plugins.Runtime) error {
	p.rt = rt
	p.conf = &conf.Nacos{}

	err := rt.GetConfig().Value(confPrefix).Scan(p.conf)
	if err != nil {
		return WrapInitError(err, "failed to scan nacos configuration")
	}

	p.setDefaultConfig()

	if err := p.validateConfig(); err != nil {
		return WrapInitError(err, "configuration validation failed")
	}

	if err := p.initComponents(); err != nil {
		return WrapInitError(err, "failed to initialize components")
	}

	if err := p.initSDKClients(); err != nil {
		return WrapInitError(err, "failed to initialize SDK clients")
	}

	atomic.StoreInt32(&p.initialized, 1)

	log.Infof("Nacos plugin initialized successfully - Server: %s, Namespace: %s",
		p.conf.ServerAddresses, p.getNamespace())

	return nil
}

// initComponents sets up metrics, retry manager, and circuit breaker.
func (p *PlugNacos) initComponents() error {
	p.metrics = NewNacosMetrics()
	p.retryManager = NewRetryManager(3, conf.DefaultRetryInterval)

	p.circuitBreaker = NewCircuitBreaker(conf.DefaultCircuitBreakerThreshold, conf.DefaultCircuitBreakerHalfOpenTimeout)

	return nil
}

// initSDKClients creates the naming and config Nacos clients based on which
// features are enabled (register/discovery → naming, config → configClient).
func (p *PlugNacos) initSDKClients() error {
	serverConfigs := p.buildServerConfigs()
	clientConfig := p.buildClientConfig()

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

// buildServerConfigs converts the configured address list to Nacos SDK ServerConfig entries.
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
			Port:        8848,
			ContextPath: p.conf.ContextPath,
		}

		if host, port, err := parseAddress(addr); err == nil {
			serverConfig.IpAddr = host
			serverConfig.Port = port
		}

		serverConfigs = append(serverConfigs, serverConfig)
	}

	return serverConfigs
}

// buildClientConfig constructs the Nacos SDK ClientConfig from plugin settings.
func (p *PlugNacos) buildClientConfig() *constant.ClientConfig {
	clientConfig := constant.NewClientConfig(
		constant.WithNamespaceId(p.getNamespaceID()),
		constant.WithTimeoutMs(uint64(p.conf.Timeout*1000)),
		constant.WithNotLoadCacheAtStart(true),
		constant.WithLogDir(p.conf.LogDir),
		constant.WithCacheDir(p.conf.CacheDir),
		constant.WithLogLevel(p.conf.LogLevel),
	)

	if p.conf.Username != "" && p.conf.Password != "" {
		clientConfig.Username = p.conf.Username
		clientConfig.Password = p.conf.Password
	} else if p.conf.AccessKey != "" && p.conf.SecretKey != "" {
		clientConfig.AccessKey = p.conf.AccessKey
		clientConfig.SecretKey = p.conf.SecretKey
	}

	if p.conf.Endpoint != "" {
		clientConfig.Endpoint = p.conf.Endpoint
	}

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

// parseAddress splits an "host:port" string into its components.
// Returns the input address and default port 8848 on any parse failure.
func parseAddress(addr string) (string, uint64, error) {
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

// splitAddress splits an address on the last colon, handling IPv6 brackets.
func splitAddress(addr string) []string {
	if strings.Contains(addr, "[") {
		// IPv6: [::1]:8848
		idx := strings.LastIndex(addr, ":")
		if idx > 0 {
			return []string{addr[:idx], addr[idx+1:]}
		}
		return []string{addr}
	}
	return strings.SplitN(addr, ":", 2)
}

// checkInitialized returns an error if the plugin has not been initialized or has been destroyed.
func (p *PlugNacos) checkInitialized() error {
	if atomic.LoadInt32(&p.initialized) == 0 {
		return ErrNotInitialized
	}
	if atomic.LoadInt32(&p.destroyed) == 1 {
		return fmt.Errorf("nacos plugin has been destroyed")
	}
	return nil
}

// StartupTasks connects to the Nacos server and starts the configured watchers.
func (p *PlugNacos) StartupTasks() error {
	ctx, cancel := p.cleanupContext()
	defer cancel()
	return p.startupTasksContext(ctx)
}

func (p *PlugNacos) publishRuntimeResources() error {
	if p.rt == nil {
		return nil
	}
	if err := lynx.RegisterControlPlaneCapabilityResources(p.rt, pluginName, p); err != nil {
		return err
	}
	if p.conf.EnableRegister {
		if registrar := p.NewServiceRegistry(); registrar != nil {
			if err := p.rt.RegisterSharedResource(pluginName+".service_registry", registrar); err != nil {
				log.Warnf("failed to register nacos service registry resource: %v", err)
			}
		}
	}
	if p.conf.EnableDiscovery {
		if discovery := p.NewServiceDiscovery(); discovery != nil {
			if err := p.rt.RegisterSharedResource(pluginName+".service_discovery", discovery); err != nil {
				log.Warnf("failed to register nacos service discovery resource: %v", err)
			}
		}
	}
	if p.namingClient != nil {
		if err := p.rt.RegisterPrivateResource("naming_client_sdk", p.namingClient); err != nil {
			log.Warnf("failed to register nacos private naming client sdk resource: %v", err)
		}
		if err := p.rt.RegisterPrivateResource("naming_client", p.namingClient); err != nil {
			log.Warnf("failed to register nacos private naming client resource: %v", err)
		}
	}
	if p.configClient != nil {
		if err := p.rt.RegisterPrivateResource("config_client_sdk", p.configClient); err != nil {
			log.Warnf("failed to register nacos private config client sdk resource: %v", err)
		}
		if err := p.rt.RegisterPrivateResource("config_client", p.configClient); err != nil {
			log.Warnf("failed to register nacos private config client resource: %v", err)
		}
	}
	return nil
}

// CheckHealth verifies the Nacos connection is responsive.
func (p *PlugNacos) CheckHealth() error {
	ctx, cancel := p.cleanupContext()
	defer cancel()
	return p.checkHealthContext(ctx)
}

// --- ControlPlane interface implementation ---

// GetNamespace returns the configured Nacos namespace.
func (p *PlugNacos) GetNamespace() string {
	return p.getNamespace()
}

// ControlPlaneCapabilities declares Nacos' explicit control plane contract.
func (p *PlugNacos) ControlPlaneCapabilities() []lynx.ControlPlaneCapability {
	capabilities := []lynx.ControlPlaneCapability{
		lynx.ControlPlaneCapabilityConfig,
		lynx.ControlPlaneCapabilityWatcher,
	}
	if p.conf != nil && p.conf.EnableRegister {
		capabilities = append(capabilities, lynx.ControlPlaneCapabilityRegistry)
	}
	if p.conf != nil && p.conf.EnableDiscovery {
		capabilities = append(capabilities, lynx.ControlPlaneCapabilityDiscovery)
	}
	return capabilities
}

// HTTPRateLimit returns nil — Nacos does not provide HTTP rate limiting.
func (p *PlugNacos) HTTPRateLimit() middleware.Middleware {
	return nil
}

// GRPCRateLimit returns nil — Nacos does not provide gRPC rate limiting.
func (p *PlugNacos) GRPCRateLimit() middleware.Middleware {
	return nil
}

// NewNodeRouter returns nil — use the discovery client directly for instance selection.
func (p *PlugNacos) NewNodeRouter(serviceName string) selector.NodeFilter {
	return nil
}

// CleanupTasks deregisters the service instance and closes Nacos clients.
func (p *PlugNacos) CleanupTasks() error {
	ctx, cancel := p.cleanupContext()
	defer cancel()
	return p.cleanupTasksContext(ctx)
}

// Configure validates and applies the new Nacos config, rolling back on error.
func (p *PlugNacos) Configure(c any) error {
	if c == nil {
		return fmt.Errorf("configuration cannot be nil")
	}

	nacosConf, ok := c.(*conf.Nacos)
	if !ok {
		return fmt.Errorf("invalid configuration type: expected *conf.Nacos, got %T", c)
	}

	oldConf := p.conf
	p.conf = nacosConf
	p.setDefaultConfig()
	if err := p.validateConfig(); err != nil {
		p.conf = oldConf
		return fmt.Errorf("nacos configuration validation failed: %w", err)
	}

	log.Infof("Nacos configuration updated successfully")
	return nil
}
