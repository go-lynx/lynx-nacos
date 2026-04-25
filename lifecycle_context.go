package nacos

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-lynx/lynx-nacos/conf"
	"github.com/go-lynx/lynx/log"
	"github.com/go-lynx/lynx/plugins"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
)

func (p *PlugNacos) IsContextAware() bool {
	return true
}

func (p *PlugNacos) InitializeContext(ctx context.Context, plugin plugins.Plugin, rt plugins.Runtime) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("initialize canceled before start: %w", err)
	}
	return p.BasePlugin.Initialize(plugin, rt)
}

func (p *PlugNacos) StartContext(ctx context.Context, plugin plugins.Plugin) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("start canceled before execution: %w", err)
	}
	if p.Status(plugin) == plugins.StatusActive {
		return plugins.ErrPluginAlreadyActive
	}

	p.SetStatus(plugins.StatusInitializing)
	p.EmitEvent(plugins.PluginEvent{
		Type:     plugins.EventPluginStarting,
		Priority: plugins.PriorityNormal,
		Source:   "StartContext",
		Category: "lifecycle",
	})

	if err := p.startupTasksContext(ctx); err != nil {
		p.SetStatus(plugins.StatusFailed)
		return plugins.NewPluginError(p.ID(), "Start", "Failed to perform startup tasks", err)
	}

	if err := p.checkHealthContext(ctx); err != nil {
		p.SetStatus(plugins.StatusFailed)
		log.Errorf("Plugin %s health check failed: %v", plugin.Name(), err)
		return fmt.Errorf("plugin %s health check failed: %w", plugin.Name(), err)
	}

	p.SetStatus(plugins.StatusActive)
	p.EmitEvent(plugins.PluginEvent{
		Type:     plugins.EventPluginStarted,
		Priority: plugins.PriorityNormal,
		Source:   "StartContext",
		Category: "lifecycle",
	})

	return nil
}

func (p *PlugNacos) StopContext(ctx context.Context, plugin plugins.Plugin) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("stop canceled before execution: %w", err)
	}
	if p.Status(plugin) != plugins.StatusActive {
		return plugins.NewPluginError(p.ID(), "Stop", "Plugin must be active to stop", plugins.ErrPluginNotActive)
	}

	p.SetStatus(plugins.StatusStopping)
	p.EmitEvent(plugins.PluginEvent{
		Type:     plugins.EventPluginStopping,
		Priority: plugins.PriorityNormal,
		Source:   "StopContext",
		Category: "lifecycle",
	})

	if err := p.cleanupTasksContext(ctx); err != nil {
		p.SetStatus(plugins.StatusFailed)
		return plugins.NewPluginError(p.ID(), "Stop", "Failed to perform cleanup tasks", err)
	}

	p.SetStatus(plugins.StatusTerminated)
	p.EmitEvent(plugins.PluginEvent{
		Type:     plugins.EventPluginStopped,
		Priority: plugins.PriorityNormal,
		Source:   "StopContext",
		Category: "lifecycle",
	})

	return nil
}

func (p *PlugNacos) startupTasksContext(ctx context.Context) (startErr error) {
	if err := p.checkInitialized(); err != nil {
		return err
	}

	if p.metrics != nil {
		p.metrics.RecordSDKOperation("startup", "start")
		defer func() {
			if p.metrics == nil {
				return
			}
			if startErr != nil {
				p.metrics.RecordSDKOperation("startup", "error")
				return
			}
			p.metrics.RecordSDKOperation("startup", "success")
		}()
	}

	if p.namingClient != nil || p.configClient != nil {
		log.Infof("Verifying Nacos server connectivity")
		if err := p.checkNacosConnectivityContext(ctx); err != nil {
			log.Errorf("Nacos connectivity verification failed: %v", err)
			return WrapInitError(err, "nacos server is unreachable during startup")
		}
		log.Infof("Nacos server connectivity verified")
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("startup cancelled before setting control plane: %w", err)
	}
	if err := currentLynxApp().SetControlPlane(p); err != nil {
		log.Errorf("Failed to set Nacos as control plane: %v", err)
		return WrapInitError(err, "failed to set control plane")
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("startup cancelled before initializing control plane config: %w", err)
	}
	cfg, err := currentLynxApp().InitControlPlaneConfig()
	if err != nil {
		log.Errorf("Failed to init Nacos control plane config: %v", err)
		return WrapInitError(err, "failed to init control plane config")
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("startup cancelled before publishing runtime resources: %w", err)
	}
	if err := p.publishRuntimeResources(); err != nil {
		log.Errorf("Failed to publish Nacos runtime resources: %v", err)
		return WrapInitError(err, "failed to publish runtime resources")
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("startup cancelled before loading dependent plugins: %w", err)
	}
	if err := currentLynxApp().GetPluginManager().LoadPlugins(cfg); err != nil {
		log.Errorf("Failed to load plugins from Nacos control plane config: %v", err)
		return WrapInitError(err, "failed to load dependent plugins")
	}

	log.Infof("Nacos plugin started successfully and set as control plane")
	return nil
}

func (p *PlugNacos) checkHealthContext(ctx context.Context) error {
	if err := p.checkInitialized(); err != nil {
		return err
	}

	var healthErr error
	if p.metrics != nil {
		p.metrics.RecordHealthCheck("nacos", "start")
		succeeded := false
		defer func() {
			if succeeded && p.metrics != nil && ctx.Err() == nil {
				p.metrics.RecordHealthCheck("nacos", "success")
			}
		}()
		defer func() {
			if healthErr == nil && ctx.Err() == nil {
				succeeded = true
			}
		}()
	}

	if p.circuitBreaker == nil || p.retryManager == nil {
		return WrapInitError(fmt.Errorf("resilience components not initialized"), "health check")
	}

	err := p.circuitBreaker.Do(func() error {
		return p.retryManager.DoWithRetryContext(ctx, func() error {
			if err := p.checkNacosConnectivityContext(ctx); err != nil {
				healthErr = err
				return err
			}
			return nil
		})
	})

	if err != nil {
		if healthErr == nil {
			healthErr = err
		}
		log.Errorf("Nacos health check failed: %v", healthErr)
		if p.metrics != nil {
			p.metrics.RecordHealthCheck("nacos", "error")
			p.metrics.RecordHealthCheckFailed("nacos", fmt.Sprintf("%T", healthErr))
		}
		return WrapOperationError(healthErr, "health check")
	}

	return nil
}

func (p *PlugNacos) checkNacosConnectivityContext(ctx context.Context) error {
	if p.namingClient == nil && p.configClient == nil {
		return fmt.Errorf("no Nacos client initialized (enable_register, enable_discovery, or enable_config required)")
	}

	if p.namingClient != nil {
		param := voSelectInstancesParam()
		err := p.runWithContext(ctx, func() error {
			_, err := p.namingClient.SelectInstances(param)
			return err
		})
		if err != nil && !stringsContainsAny(err.Error(), "not found", "no instance", "404") {
			return fmt.Errorf("naming client connectivity check failed: %w", err)
		}
	}

	if p.configClient != nil {
		param := voConfigParam()
		err := p.runWithContext(ctx, func() error {
			_, err := p.configClient.GetConfig(param)
			return err
		})
		if err != nil && !stringsContainsAny(err.Error(), "config not found", "404") {
			return fmt.Errorf("config client connectivity check failed: %w", err)
		}
	}

	return nil
}

func (p *PlugNacos) cleanupTasksContext(ctx context.Context) error {
	atomic.StoreInt32(&p.destroyed, 1)

	var errs []error

	p.watcherMutex.Lock()
	for key, watcher := range p.configWatchers {
		if watcher == nil {
			continue
		}
		if err := p.stopConfigWatcher(ctx, key, watcher); err != nil {
			log.Errorf("failed to stop config watcher %s: %v", key, err)
			errs = append(errs, err)
		}
	}
	p.configWatchers = make(map[string]*ConfigWatcher)
	p.watcherMutex.Unlock()

	p.cacheMutex.Lock()
	p.serviceCache = make(map[string]interface{})
	p.configCache = make(map[string]interface{})
	p.cacheMutex.Unlock()

	if err := p.closeSDKClients(ctx); err != nil {
		errs = append(errs, err)
	}
	atomic.StoreInt32(&p.initialized, 0)

	if err := ctx.Err(); err != nil {
		errs = append(errs, fmt.Errorf("nacos cleanup cancelled: %w", err))
	}

	if joined := errors.Join(errs...); joined != nil {
		return joined
	}

	log.Infof("Nacos plugin cleaned up successfully")
	return nil
}

func (p *PlugNacos) closeSDKClients(ctx context.Context) error {
	var errs []error

	if p.namingClient != nil {
		namingClient := p.namingClient
		if err := p.runWithContext(ctx, func() error {
			namingClient.CloseClient()
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("close nacos naming client: %w", err))
		} else {
			p.namingClient = nil
		}
	}

	if p.configClient != nil {
		configClient := p.configClient
		if err := p.runWithContext(ctx, func() error {
			configClient.CloseClient()
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("close nacos config client: %w", err))
		} else {
			p.configClient = nil
		}
	}

	return errors.Join(errs...)
}

func (p *PlugNacos) stopConfigWatcher(ctx context.Context, key string, watcher *ConfigWatcher) (err error) {
	return p.runWithContext(ctx, func() error {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic while stopping config watcher %s: %v", key, r)
			}
		}()
		if watcher.cancel != nil {
			watcher.cancel()
		}
		if watcher.watcher != nil {
			if stopErr := watcher.watcher.Stop(); stopErr != nil {
				return fmt.Errorf("stop config watcher %s: %w", key, stopErr)
			}
		}
		return nil
	})
}

func (p *PlugNacos) runWithContext(ctx context.Context, operation func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	done := make(chan error, 1)
	go func() {
		done <- operation()
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *PlugNacos) cleanupContext() (context.Context, context.CancelFunc) {
	timeout := time.Duration(conf.DefaultTimeout) * time.Second
	if p.conf != nil && p.conf.Timeout > 0 {
		timeout = time.Duration(p.conf.Timeout) * time.Second
	}
	return context.WithTimeout(context.Background(), timeout)
}

func stringsContainsAny(value string, substrings ...string) bool {
	for _, substring := range substrings {
		if strings.Contains(value, substring) {
			return true
		}
	}
	return false
}

func voSelectInstancesParam() vo.SelectInstancesParam {
	return vo.SelectInstancesParam{
		ServiceName: "lynx-nacos-health-probe",
		GroupName:   conf.DefaultGroup,
	}
}

func voConfigParam() vo.ConfigParam {
	return vo.ConfigParam{
		DataId: "lynx-nacos-health-probe",
		Group:  conf.DefaultGroup,
	}
}
