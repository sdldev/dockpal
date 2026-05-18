package traefik

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type TraefikConfig struct {
	HTTP HTTPConfig `yaml:"http"`
}

type HTTPConfig struct {
	Routers  map[string]Router  `yaml:"routers,omitempty"`
	Services map[string]Service `yaml:"services,omitempty"`
}

type Router struct {
	Rule        string     `yaml:"rule"`
	Service     string     `yaml:"service"`
	EntryPoints []string   `yaml:"entryPoints,omitempty"`
	TLS         *TLSConfig `yaml:"tls,omitempty"`
}

type TLSConfig struct {
	CertResolver string `yaml:"certResolver"`
}

type Service struct {
	LoadBalancer LoadBalancer `yaml:"loadBalancer"`
}

type LoadBalancer struct {
	Servers []Server `yaml:"servers"`
}

type Server struct {
	URL string `yaml:"url"`
}

const configPath = "/opt/dockpal/traefik/dynamic.yml"

func GenerateConfig(domain, serviceName string, port int) error {
	// Check if config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		log.Printf("WARNING: Traefik config file does not exist at %s, skipping config generation", configPath)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("failed to create traefik dir: %w", err)
	}

	var config TraefikConfig
	data, err := os.ReadFile(configPath)
	if err == nil {
		yaml.Unmarshal(data, &config)
	}

	if config.HTTP.Routers == nil {
		config.HTTP.Routers = make(map[string]Router)
	}
	if config.HTTP.Services == nil {
		config.HTTP.Services = make(map[string]Service)
	}

	routerName := fmt.Sprintf("%s-router", serviceName)
	serviceURL := fmt.Sprintf("http://%s:%d", serviceName, port)

	config.HTTP.Routers[routerName] = Router{
		Rule:        fmt.Sprintf("Host(`%s`)", domain),
		Service:     serviceName,
		EntryPoints: []string{"websecure"},
		TLS: &TLSConfig{
			CertResolver: "letsencrypt",
		},
	}

	config.HTTP.Services[serviceName] = Service{
		LoadBalancer: LoadBalancer{
			Servers: []Server{{URL: serviceURL}},
		},
	}

	out, err := yaml.Marshal(&config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	return os.WriteFile(configPath, out, 0644)
}

func RemoveDomain(serviceName string) error {
	// Check if config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		log.Printf("WARNING: Traefik config file does not exist at %s, skipping domain removal", configPath)
		return nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil
	}

	var config TraefikConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return err
	}

	routerName := fmt.Sprintf("%s-router", serviceName)
	delete(config.HTTP.Routers, routerName)
	delete(config.HTTP.Services, serviceName)

	out, err := yaml.Marshal(&config)
	if err != nil {
		return err
	}

	return os.WriteFile(configPath, out, 0644)
}
