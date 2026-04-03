# Nacos Plugin for Lynx Framework

This plugin provides Nacos service registration, discovery, and configuration management functionality for the Lynx framework.

## Features

- ✅ **Service Registration**: Register service instances to Nacos
- ✅ **Service Discovery**: Discover service instances from Nacos
- ✅ **Configuration Management**: Get and watch configuration from Nacos
- ✅ **Multi-Config Support**: Load multiple configuration sources
- ✅ **Dynamic Configuration**: Real-time configuration updates
- ✅ **Health Check**: Built-in health check support
- ✅ **Authentication**: Support username/password and access key/secret key
- ✅ **Namespace Support**: Multi-tenant namespace isolation

## Installation

```bash
go get github.com/go-lynx/lynx/plugins/nacos
```

## Quick Start

### 1. Configuration

```yaml
lynx:
  nacos:
    # Nacos server addresses
    server_addresses: "127.0.0.1:8848"
    
    # Namespace
    namespace: "public"
    
    # Authentication (optional)
    username: "nacos"
    password: "nacos"
    
    # Feature flags
    enable_register: true
    enable_discovery: true
    enable_config: true
    
    # Service configuration
    service_config:
      service_name: "my-service"
      group: "DEFAULT_GROUP"
      cluster: "DEFAULT"
```

### 2. Usage

```go
package main

import (
    "github.com/go-lynx/lynx/app"
    "github.com/go-lynx/lynx/boot"
    _ "github.com/go-lynx/lynx/plugins/nacos" // Import to register plugin
)

func main() {
    boot.LynxApplication(wireApp).Run()
}

func wireApp() (*kratos.App, error) {
    // Nacos plugin will be automatically loaded from configuration
    // ...
}
```

## Configuration Reference

### Configuration Options

#### Connection & Auth
- `server_addresses` (string, required): Nacos server addresses (comma-separated). Example: `"127.0.0.1:8848"`
- `endpoint` (string, optional): Endpoint for Nacos server (alternative to `server_addresses`). Example: `"http://nacos.example.com"`
- `context_path` (string, optional): Context path for Nacos server. Example: `"/nacos"`
- `region_id` (string, optional): Region ID. Example: `"us-east-1"`
- `namespace_id` (string, optional): Namespace ID. Example: `"your-namespace-id"`
- `namespace` (string, default: `"public"`): Namespace name (alternative to `namespace_id`).
- `username` (string, optional): Username for authentication.
- `password` (string, optional): Password for authentication.
- `access_key` (string, optional): Access key for authentication.
- `secret_key` (string, optional): Secret key for authentication.

#### Service Instance
- `weight` (double, default: `1.0`): Service instance weight. Example: `1.0`
- `metadata` (map<string, string>, optional): Service instance metadata. Example: `{"version": "v1.0.0"}`

#### Feature Flags
- `enable_register` (bool, default: `false`): Enable service registration to Nacos.
- `enable_discovery` (bool, default: `false`): Enable service discovery from Nacos.
- `enable_config` (bool, default: `false`): Enable configuration management from Nacos.

#### Settings & Logging
- `timeout` (uint64, default: `5`): Connection timeout in seconds.
- `notify_timeout` (uint64, default: `3000`): Notify timeout in milliseconds.
- `log_level` (string, default: `"info"`): Log level (debug, info, warn, error).
- `log_dir` (string, optional): Log directory. Example: `"./logs/nacos"`
- `cache_dir` (string, optional): Cache directory. Example: `"./cache/nacos"`

#### Service Configuration (for registration)
- `service_config.service_name` (string, required): Service name for registration.
- `service_config.group` (string, default: `"DEFAULT_GROUP"`): Group name.
- `service_config.cluster` (string, default: `"DEFAULT"`): Cluster name.
- `service_config.health_check` (bool, default: `true`): Enable health check.
- `service_config.health_check_interval` (uint64, default: `5`): Health check interval in seconds.
- `service_config.health_check_timeout` (uint64, default: `3`): Health check timeout in seconds.
- `service_config.health_check_type` (string, default: `"tcp"`): Health check type (`none`, `tcp`, `http`, `mysql`).
- `service_config.health_check_url` (string, optional): Health check URL (for http type).

#### Additional Configuration Sources
- `additional_configs` (repeated ConfigSource, optional): List of additional configuration sources to load.
  - `data_id` (string, required): Configuration data ID.
  - `group` (string, default: `"DEFAULT_GROUP"`): Configuration group.
  - `format` (string, default: `"yaml"`): Configuration format (`yaml`, `json`, `properties`, `xml`).

### Basic Configuration Example

## API Reference

### Service Registration and Discovery

```go
// Get Nacos plugin
nacosPlugin := app.Lynx().GetPluginManager().GetPlugin("nacos.control.plane")
if nacosPlugin == nil {
    return fmt.Errorf("nacos plugin not found")
}

nacos, ok := nacosPlugin.(*nacos.PlugNacos)
if !ok {
    return fmt.Errorf("invalid plugin type")
}

// Get service registry
registrar := nacos.NewServiceRegistry()

// Register service
instance := &registry.ServiceInstance{
    ID:       "instance-1",
    Name:     "my-service",
    Version:  "v1.0.0",
    Metadata: map[string]string{"env": "production"},
    Endpoints: []string{"http://127.0.0.1:8080"},
}
err := registrar.Register(context.Background(), instance)

// Get service discovery
discovery := nacos.NewServiceDiscovery()

// Get service instances
instances, err := discovery.GetService(context.Background(), "my-service")

// Watch service changes
watcher, err := discovery.Watch(context.Background(), "my-service")
```

### Configuration Management

```go
// Get configuration
configSource, err := nacos.GetConfig("application.yaml", "DEFAULT_GROUP")
if err != nil {
    return err
}

// Load configuration
kvs, err := configSource.Load()

// Watch configuration changes
watcher, err := configSource.Watch()
if err != nil {
    return err
}

// Get next change
kvs, err := watcher.Next()
```

### Multi-Config Loading

```go
// Get all configuration sources
sources, err := nacos.GetConfigSources()
if err != nil {
    return err
}

// Load all configurations
for _, source := range sources {
    kvs, err := source.Load()
    // Process configuration
}
```

## Health Check

Nacos supports multiple health check types:

- **none**: No health check
- **tcp**: TCP connection check
- **http**: HTTP endpoint check
- **mysql**: MySQL connection check

```yaml
service_config:
  health_check: true
  health_check_type: "http"
  health_check_url: "http://127.0.0.1:8080/health"
  health_check_interval: 5
  health_check_timeout: 3
```

## Authentication

Nacos supports two authentication methods:

### Username/Password

```yaml
nacos:
  username: "nacos"
  password: "nacos"
```

### Access Key/Secret Key

```yaml
nacos:
  access_key: "your-access-key"
  secret_key: "your-secret-key"
```

## Namespace Support

Nacos supports namespace isolation:

```yaml
nacos:
  namespace_id: "your-namespace-id"
  # Or use namespace name
  namespace: "production"
```

## Cluster Support

Nacos supports service clustering:

```yaml
nacos:
  service_config:
    cluster: "cluster-a"  # Default: DEFAULT
```

## Best Practices

1. **Use Namespace for Environment Isolation**
   ```yaml
   namespace: "production"  # or "development", "staging"
   ```

2. **Enable Health Check**
   ```yaml
   service_config:
     health_check: true
     health_check_type: "http"
     health_check_url: "/health"
   ```

3. **Use Multiple Server Addresses for High Availability**
   ```yaml
   server_addresses: "nacos1:8848,nacos2:8848,nacos3:8848"
   ```

4. **Configure Appropriate Timeouts**
   ```yaml
   timeout: 5  # Connection timeout
   notify_timeout: 3000  # Notification timeout
   ```

5. **Use Metadata for Service Information**
   ```yaml
   metadata:
     version: "v1.0.0"
     region: "us-east-1"
     zone: "zone-a"
   ```

## Troubleshooting

### Connection Issues

- Check Nacos server is running and accessible
- Verify server addresses are correct
- Check network connectivity
- Verify authentication credentials

### Configuration Not Loading

- Verify `enable_config` is set to `true`
- Check dataId and group are correct
- Verify namespace configuration
- Check Nacos console for configuration existence

### Service Registration Failed

- Verify `enable_register` is set to `true`
- Check service name is valid
- Verify endpoint format is correct
- Check Nacos server logs

## Examples

See `examples/` directory for complete examples.

## License

Apache License 2.0

