package docker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/sdldev/dockpal/internal/validator"
	"gopkg.in/yaml.v3"
)

// ComposeFile represents a parsed docker-compose YAML file.
type ComposeFile struct {
	Version  string                    `yaml:"version,omitempty"`
	Services map[string]ComposeService `yaml:"services"`
}

// ComposeService represents a single service definition in docker-compose.
type ComposeService struct {
	Image       string      `yaml:"image"`
	Ports       []string    `yaml:"ports,omitempty"`
	Volumes     []string    `yaml:"volumes,omitempty"`
	Environment interface{} `yaml:"environment,omitempty"`
	Networks    []string    `yaml:"networks,omitempty"`
	DependsOn   interface{} `yaml:"depends_on,omitempty"`
	Restart     string      `yaml:"restart,omitempty"`
	Labels      interface{} `yaml:"labels,omitempty"`
	Command     interface{} `yaml:"command,omitempty"`
}

// PortBinding represents a parsed port mapping.
type PortBinding struct {
	HostPort      int
	ContainerPort int
	Protocol      string
}

// VolumeMount represents a parsed volume mount.
type VolumeMount struct {
	HostPath      string
	ContainerPath string
	ReadOnly      bool
}

// ParseComposeFile parses a docker-compose YAML string into a ComposeFile struct.
func ParseComposeFile(yamlContent string) (*ComposeFile, error) {
	var cf ComposeFile
	yamlContent = SubstituteComposeEnv(yamlContent)
	if err := yaml.Unmarshal([]byte(yamlContent), &cf); err != nil {
		return nil, fmt.Errorf("invalid compose YAML: %w", err)
	}
	if cf.Services == nil || len(cf.Services) == 0 {
		return nil, fmt.Errorf("no services defined in compose file")
	}
	return &cf, nil
}

// ParsePort parses a port specification string into a PortBinding.
// Supports formats: "80", "8080:80", "8080:80/tcp", "8080:80/udp"
func ParsePort(spec string) (PortBinding, error) {
	pb := PortBinding{Protocol: "tcp"}

	// Check for protocol suffix
	if idx := strings.LastIndex(spec, "/"); idx != -1 {
		pb.Protocol = spec[idx+1:]
		spec = spec[:idx]
	}

	parts := strings.Split(spec, ":")
	switch len(parts) {
	case 1:
		port, err := strconv.Atoi(parts[0])
		if err != nil {
			return pb, fmt.Errorf("invalid port: %s", spec)
		}
		pb.ContainerPort = port
		pb.HostPort = port
	case 2:
		host, err := strconv.Atoi(parts[0])
		if err != nil {
			return pb, fmt.Errorf("invalid host port: %s", parts[0])
		}
		cport, err := strconv.Atoi(parts[1])
		if err != nil {
			return pb, fmt.Errorf("invalid container port: %s", parts[1])
		}
		pb.HostPort = host
		pb.ContainerPort = cport
	case 3:
		// ip:hostPort:containerPort format - ignore IP binding
		host, err := strconv.Atoi(parts[1])
		if err != nil {
			return pb, fmt.Errorf("invalid host port: %s", parts[1])
		}
		cport, err := strconv.Atoi(parts[2])
		if err != nil {
			return pb, fmt.Errorf("invalid container port: %s", parts[2])
		}
		pb.HostPort = host
		pb.ContainerPort = cport
	default:
		return pb, fmt.Errorf("invalid port format: %s", spec)
	}
	return pb, nil
}

// ParseVolume parses a volume specification string into a VolumeMount.
// Supports formats: "/container", "/host:/container", "/host:/container:ro"
func ParseVolume(spec string) (VolumeMount, error) {
	vm := VolumeMount{}
	parts := strings.Split(spec, ":")
	switch len(parts) {
	case 1:
		vm.ContainerPath = parts[0]
	case 2:
		vm.HostPath = parts[0]
		vm.ContainerPath = parts[1]
	case 3:
		vm.HostPath = parts[0]
		vm.ContainerPath = parts[1]
		vm.ReadOnly = parts[2] == "ro"
	default:
		return vm, fmt.Errorf("invalid volume format: %s", spec)
	}
	return vm, nil
}

// ParseEnvironment converts environment variable definitions (list or map format) into a string slice.
func ParseEnvironment(env interface{}) []string {
	switch v := env.(type) {
	case []interface{}:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	case map[string]interface{}:
		result := make([]string, 0, len(v))
		for key, val := range v {
			if val == nil {
				result = append(result, key)
			} else {
				result = append(result, fmt.Sprintf("%s=%v", key, val))
			}
		}
		return result
	default:
		return nil
	}
}

// ResolveStartOrder performs a topological sort on services based on depends_on.
// Returns services in start order (dependencies first).
func ResolveStartOrder(cf *ComposeFile) ([]string, error) {
	deps := make(map[string][]string)
	for name, svc := range cf.Services {
		deps[name] = parseDependsOn(svc.DependsOn)
	}
	return topologicalSort(deps)
}

func parseDependsOn(dep interface{}) []string {
	switch v := dep.(type) {
	case []interface{}:
		result := make([]string, 0, len(v))
		for _, d := range v {
			if s, ok := d.(string); ok {
				result = append(result, s)
			}
		}
		return result
	case map[string]interface{}:
		result := make([]string, 0, len(v))
		for name := range v {
			result = append(result, name)
		}
		return result
	default:
		return nil
	}
}

func topologicalSort(deps map[string][]string) ([]string, error) {
	visited := make(map[string]bool)
	inStack := make(map[string]bool)
	var order []string

	var visit func(string) error
	visit = func(node string) error {
		if inStack[node] {
			return fmt.Errorf("circular dependency detected at %s", node)
		}
		if visited[node] {
			return nil
		}
		inStack[node] = true
		for _, dep := range deps[node] {
			if err := visit(dep); err != nil {
				return err
			}
		}
		inStack[node] = false
		visited[node] = true
		order = append(order, node)
		return nil
	}

	for name := range deps {
		if err := visit(name); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// parseLabels converts a labels field (map or list) into a map[string]string.
func parseLabels(labels interface{}) map[string]string {
	result := make(map[string]string)
	switch v := labels.(type) {
	case map[string]interface{}:
		for key, val := range v {
			result[key] = fmt.Sprintf("%v", val)
		}
	case []interface{}:
		for _, item := range v {
			if s, ok := item.(string); ok {
				parts := strings.SplitN(s, "=", 2)
				if len(parts) == 2 {
					result[parts[0]] = parts[1]
				} else {
					result[parts[0]] = ""
				}
			}
		}
	}
	return result
}

// pullImageIfNeeded pulls the image if it's not available locally, or always if force is true.
// If registryAuth is non-empty, it uses authenticated pull.
func (c *Client) pullImageIfNeeded(ctx context.Context, image string, registryAuth string, force bool) error {
	if !force {
		_, err := c.cli.ImageInspect(ctx, image)
		if err == nil {
			return nil // image exists locally
		}
	}
	if registryAuth != "" {
		return c.PullImageWithAuth(ctx, image, registryAuth)
	}
	return c.PullImage(ctx, image)
}

func composeBaseDir() string {
	dataDir := os.Getenv("DOCKPAL_DATA_DIR")
	if dataDir == "" {
		dataDir = "/opt/dockpal/data"
	}
	return filepath.Join(filepath.Dir(dataDir), "compose")
}

func composeProjectDir(projectName string) (string, error) {
	if err := validator.ValidateContainerName(projectName); err != nil {
		return "", fmt.Errorf("invalid project name: %w", err)
	}
	if strings.Contains(projectName, "..") || strings.ContainsAny(projectName, `/\\`) {
		return "", fmt.Errorf("invalid project name")
	}
	base := filepath.Clean(composeBaseDir())
	composeDir := filepath.Clean(filepath.Join(base, projectName))
	if composeDir == base || !strings.HasPrefix(composeDir, base+string(os.PathSeparator)) {
		return "", fmt.Errorf("invalid project path")
	}
	return composeDir, nil
}

// writeComposeFile saves the compose YAML to disk.
func writeComposeFile(projectName, composeYAML string) error {
	composeDir, err := composeProjectDir(projectName)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(composeDir, 0755); err != nil {
		return fmt.Errorf("failed to create compose directory: %w", err)
	}
	composeFilePath := filepath.Join(composeDir, "docker-compose.yml")
	if err := os.WriteFile(composeFilePath, []byte(composeYAML), 0644); err != nil {
		return fmt.Errorf("failed to write compose file: %w", err)
	}
	return nil
}

// createAndStartService creates and starts a single service container.
func (c *Client) createAndStartService(ctx context.Context, projectName, svcName string, svc ComposeService, cf *ComposeFile) error {
	composeDir, err := composeProjectDir(projectName)
	if err != nil {
		return err
	}
	composeFilePath := filepath.Join(composeDir, "docker-compose.yml")
	baseLabels := map[string]string{
		"dockpal.managed": "true",
		"dockpal.project": projectName,
		"dockpal.compose": composeFilePath,
	}

	svcLabels := make(map[string]string)
	for k, v := range baseLabels {
		svcLabels[k] = v
	}
	svcLabels["dockpal.service"] = svcName
	userLabels := parseLabels(svc.Labels)
	for k, v := range userLabels {
		svcLabels[k] = v
	}

	envVars := ParseEnvironment(svc.Environment)

	portBindings := make(network.PortMap)
	exposedPorts := make(network.PortSet)
	for _, portSpec := range svc.Ports {
		pb, err := ParsePort(portSpec)
		if err != nil {
			return fmt.Errorf("service %s: %w", svcName, err)
		}
		portKey := network.MustParsePort(fmt.Sprintf("%d/%s", pb.ContainerPort, pb.Protocol))
		exposedPorts[portKey] = struct{}{}
		portBindings[portKey] = append(portBindings[portKey], network.PortBinding{
			HostPort: strconv.Itoa(pb.HostPort),
		})
	}

	var binds []string
	for _, volSpec := range svc.Volumes {
		vm, err := ParseVolume(volSpec)
		if err != nil {
			return fmt.Errorf("service %s: %w", svcName, err)
		}
		bind := vm.HostPath + ":" + vm.ContainerPath
		if vm.ReadOnly {
			bind += ":ro"
		}
		if vm.HostPath != "" {
			binds = append(binds, bind)
		}
	}

	// Default empty/unknown to unless-stopped so the app survives a host
	// reboot; deploy handlers normalize explicit no/on-failure beforehand.
	restartPolicy := NormalizeRestartPolicy(svc.Restart, false)

	containerConfig := &container.Config{
		Labels:       svcLabels,
		Env:          envVars,
		ExposedPorts: exposedPorts,
	}

	hostConfig := &container.HostConfig{
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyMode(restartPolicy)},
		PortBindings:  portBindings,
		Binds:         binds,
	}

	var networkConfig *network.NetworkingConfig
	if len(svc.Networks) > 0 {
		endpointsConfig := make(map[string]*network.EndpointSettings)
		for _, netName := range svc.Networks {
			endpointsConfig[netName] = &network.EndpointSettings{}
		}
		networkConfig = &network.NetworkingConfig{
			EndpointsConfig: endpointsConfig,
		}
	}

	containerName := fmt.Sprintf("%s_%s", projectName, svcName)

	createOpts := client.ContainerCreateOptions{
		Name:             containerName,
		Image:            svc.Image,
		Config:           containerConfig,
		HostConfig:       hostConfig,
		NetworkingConfig: networkConfig,
	}

	result, err := c.cli.ContainerCreate(ctx, createOpts)
	if err != nil {
		return fmt.Errorf("failed to create container for %s: %w", svcName, err)
	}

	if _, err := c.cli.ContainerStart(ctx, result.ID, client.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("failed to start container %s: %w", svcName, err)
	}
	return nil
}

// AuthHeaderFunc is a function that returns the registry auth header for a given image reference.
// Returns empty string if no credentials match (fallback to unauthenticated pull).
type AuthHeaderFunc func(imageRef string) (string, error)

// DeployCompose deploys services defined in a docker-compose YAML string.
// If getAuthHeader is non-nil, it will be called per image to get registry credentials.
// If forcePull is true, images are always pulled even if they already exist locally.
func (c *Client) DeployCompose(ctx context.Context, projectName, composeYAML string, getAuthHeader AuthHeaderFunc, forcePull bool) error {
	composeYAML = SubstituteComposeEnv(composeYAML)

	if err := writeComposeFile(projectName, composeYAML); err != nil {
		return err
	}

	cf, err := ParseComposeFile(composeYAML)
	if err != nil {
		return fmt.Errorf("failed to parse compose: %w", err)
	}

	startOrder, err := ResolveStartOrder(cf)
	if err != nil {
		return fmt.Errorf("failed to resolve service start order: %w", err)
	}

	for _, svcName := range startOrder {
		svc := cf.Services[svcName]
		registryAuth := ""
		if getAuthHeader != nil {
			auth, err := getAuthHeader(svc.Image)
			if err == nil {
				registryAuth = auth
			}
		}
		if err := c.pullImageIfNeeded(ctx, svc.Image, registryAuth, forcePull); err != nil {
			return fmt.Errorf("failed to pull image for %s: %w", svcName, err)
		}
		if err := c.createAndStartService(ctx, projectName, svcName, svc, cf); err != nil {
			return err
		}
	}

	return nil
}

// StopCompose stops all containers belonging to a compose project.
func (c *Client) StopCompose(ctx context.Context, projectName string) error {
	if _, err := composeProjectDir(projectName); err != nil {
		return err
	}
	f := make(client.Filters)
	f = f.Add("label", fmt.Sprintf("dockpal.project=%s", projectName))
	result, err := c.cli.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: f})
	if err != nil {
		return err
	}

	timeout := DefaultStopTimeout
	for _, ctr := range result.Items {
		c.cli.ContainerStop(ctx, ctr.ID, client.ContainerStopOptions{Timeout: &timeout})
	}

	return nil
}

// RemoveCompose removes all containers and files belonging to a compose project.
func (c *Client) RemoveCompose(ctx context.Context, projectName string) error {
	composeDir, err := composeProjectDir(projectName)
	if err != nil {
		return err
	}
	f := make(client.Filters)
	f = f.Add("label", fmt.Sprintf("dockpal.project=%s", projectName))
	result, err := c.cli.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: f})
	if err != nil {
		return err
	}

	for _, ctr := range result.Items {
		c.cli.ContainerRemove(ctx, ctr.ID, client.ContainerRemoveOptions{Force: true})
	}

	os.RemoveAll(composeDir)

	return nil
}

// SetServiceLabel returns the compose YAML with the label `key` set to
// `value` on every service. When `value == ""` the label is removed.
// Other service fields and other labels are preserved.
//
// The function performs a yaml.v3 round-trip through *yaml.Node so
// comments and field ordering are kept where the encoder allows. Both
// the mapping form (`labels: {a: b}`) and the sequence form
// (`labels: ["a=b"]`) are supported. In sequence form, the entry is
// written as `<key>=<value>` and a matching prefix `<key>=` is removed
// when value is empty.
//
// Returns an error when the input is not valid YAML or has no
// top-level `services:` mapping.
func SetServiceLabel(composeYAML, key, value string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("label key must not be empty")
	}
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(composeYAML), &root); err != nil {
		return "", fmt.Errorf("invalid compose YAML: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return "", fmt.Errorf("empty compose YAML")
	}
	rootMap := root.Content[0]
	if rootMap.Kind != yaml.MappingNode {
		return "", fmt.Errorf("compose root is not a mapping")
	}
	servicesNode := findYAMLMapValue(rootMap, "services")
	if servicesNode == nil {
		return "", fmt.Errorf("no services defined in compose file")
	}
	if servicesNode.Kind != yaml.MappingNode {
		return "", fmt.Errorf("services is not a mapping")
	}
	if len(servicesNode.Content) == 0 {
		return "", fmt.Errorf("no services defined in compose file")
	}

	for i := 0; i+1 < len(servicesNode.Content); i += 2 {
		svc := servicesNode.Content[i+1]
		if svc.Kind != yaml.MappingNode {
			continue
		}
		if err := setServiceLabelOnNode(svc, key, value); err != nil {
			return "", err
		}
	}

	out, err := yaml.Marshal(&root)
	if err != nil {
		return "", fmt.Errorf("failed to marshal compose YAML: %w", err)
	}
	return string(out), nil
}

// envVarPattern matches ${KEY} and ${KEY:-default} placeholders in compose files.
var envVarPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-[^}]*)?\}`)

// ApplyEnvVariables returns compose YAML with env applied to every service.
// Placeholders in the form ${KEY} and ${KEY:-default} are replaced before
// YAML parsing, and the final environment map is written to each service's
// environment section.
func ApplyEnvVariables(composeYAML string, env map[string]string) (string, error) {
	if len(env) == 0 {
		return composeYAML, nil
	}
	composeYAML = envVarPattern.ReplaceAllStringFunc(composeYAML, func(match string) string {
		parts := envVarPattern.FindStringSubmatch(match)
		if parts == nil {
			return match
		}
		key := parts[1]
		if val, ok := env[key]; ok {
			return val
		}
		// If key not in env map but default exists, use the default
		if len(parts) > 2 && strings.HasPrefix(parts[2], ":-") {
			return parts[2][2:] // strip ":-" prefix
		}
		return match
	})
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(composeYAML), &root); err != nil {
		return "", fmt.Errorf("invalid compose YAML: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return "", fmt.Errorf("empty compose YAML")
	}
	rootMap := root.Content[0]
	if rootMap.Kind != yaml.MappingNode {
		return "", fmt.Errorf("compose root is not a mapping")
	}
	servicesNode := findYAMLMapValue(rootMap, "services")
	if servicesNode == nil || servicesNode.Kind != yaml.MappingNode || len(servicesNode.Content) == 0 {
		return "", fmt.Errorf("no services defined in compose file")
	}
	for i := 0; i+1 < len(servicesNode.Content); i += 2 {
		svc := servicesNode.Content[i+1]
		if svc.Kind != yaml.MappingNode {
			continue
		}
		if err := setServiceEnvironmentOnNode(svc, env); err != nil {
			return "", err
		}
	}
	out, err := yaml.Marshal(&root)
	if err != nil {
		return "", fmt.Errorf("failed to marshal compose YAML: %w", err)
	}
	return string(out), nil
}

func setServiceEnvironmentOnNode(svc *yaml.Node, env map[string]string) error {
	envVal := findYAMLMapValue(svc, "environment")
	if envVal == nil {
		svc.Content = append(svc.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "environment"},
			newEnvMappingNode(env),
		)
		return nil
	}
	switch envVal.Kind {
	case yaml.MappingNode:
		for key, value := range env {
			setYAMLMapString(envVal, key, value)
		}
		return nil
	case yaml.SequenceNode:
		for key, value := range env {
			setYAMLSequenceEnv(envVal, key, value)
		}
		return nil
	case yaml.ScalarNode:
		envVal.Kind = yaml.MappingNode
		envVal.Tag = "!!map"
		envVal.Value = ""
		envVal.Style = 0
		envVal.Content = newEnvMappingNode(env).Content
		return nil
	default:
		return fmt.Errorf("unsupported environment node kind %d", envVal.Kind)
	}
}

func newEnvMappingNode(env map[string]string) *yaml.Node {
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for key, value := range env {
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
			newEnvStringNode(value),
		)
	}
	return node
}

func setYAMLMapString(node *yaml.Node, key, value string) {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			node.Content[i+1] = newEnvStringNode(value)
			return
		}
	}
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		newEnvStringNode(value),
	)
}

func setYAMLSequenceEnv(node *yaml.Node, key, value string) {
	prefix := key + "="
	entry := prefix + value
	for _, item := range node.Content {
		if item.Kind == yaml.ScalarNode && (item.Value == key || strings.HasPrefix(item.Value, prefix)) {
			item.Tag = "!!str"
			item.Value = entry
			return
		}
	}
	node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: entry})
}

func newEnvStringNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: yaml.DoubleQuotedStyle}
}

// setServiceLabelOnNode sets or removes a label on a single service mapping node.
func setServiceLabelOnNode(svc *yaml.Node, key, value string) error {
	labelsVal := findYAMLMapValue(svc, "labels")
	if labelsVal == nil {
		if value == "" {
			return nil
		}
		svc.Content = append(svc.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "labels"},
			&yaml.Node{
				Kind: yaml.MappingNode,
				Tag:  "!!map",
				Content: []*yaml.Node{
					{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
					newLabelStringNode(value),
				},
			},
		)
		return nil
	}

	switch labelsVal.Kind {
	case yaml.MappingNode:
		idx := -1
		for i := 0; i+1 < len(labelsVal.Content); i += 2 {
			if labelsVal.Content[i].Value == key {
				idx = i
				break
			}
		}
		if value == "" {
			if idx >= 0 {
				labelsVal.Content = append(labelsVal.Content[:idx], labelsVal.Content[idx+2:]...)
			}
			if len(labelsVal.Content) == 0 {
				removeYAMLMapEntry(svc, "labels")
			}
			return nil
		}
		if idx >= 0 {
			vn := labelsVal.Content[idx+1]
			vn.Kind = yaml.ScalarNode
			vn.Tag = "!!str"
			vn.Value = value
			// Force quoted style to keep label values like "true" as strings.
			vn.Style = yaml.DoubleQuotedStyle
			return nil
		}
		labelsVal.Content = append(labelsVal.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
			newLabelStringNode(value),
		)
		return nil

	case yaml.SequenceNode:
		prefix := key + "="
		idx := -1
		for i, item := range labelsVal.Content {
			if item.Kind != yaml.ScalarNode {
				continue
			}
			if item.Value == key || strings.HasPrefix(item.Value, prefix) {
				idx = i
				break
			}
		}
		if value == "" {
			if idx >= 0 {
				labelsVal.Content = append(labelsVal.Content[:idx], labelsVal.Content[idx+1:]...)
			}
			if len(labelsVal.Content) == 0 {
				removeYAMLMapEntry(svc, "labels")
			}
			return nil
		}
		entry := key + "=" + value
		if idx >= 0 {
			labelsVal.Content[idx].Tag = "!!str"
			labelsVal.Content[idx].Value = entry
			return nil
		}
		labelsVal.Content = append(labelsVal.Content, &yaml.Node{
			Kind: yaml.ScalarNode, Tag: "!!str", Value: entry,
		})
		return nil

	case yaml.ScalarNode:
		// `labels:` with a null/empty scalar - upgrade to mapping if needed.
		if value == "" {
			return nil
		}
		labelsVal.Kind = yaml.MappingNode
		labelsVal.Tag = "!!map"
		labelsVal.Value = ""
		labelsVal.Style = 0
		labelsVal.Content = []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
			newLabelStringNode(value),
		}
		return nil

	default:
		return fmt.Errorf("unsupported labels node kind %d", labelsVal.Kind)
	}
}

// newLabelStringNode builds a yaml string node, double-quoted so values
// like "true" are emitted as strings rather than booleans.
func newLabelStringNode(value string) *yaml.Node {
	return &yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!str",
		Value: value,
		Style: yaml.DoubleQuotedStyle,
	}
}

// findYAMLMapValue returns the value node for `key` in a mapping node, or nil.
func findYAMLMapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// removeYAMLMapEntry deletes the key/value pair for `key` from a mapping node.
func removeYAMLMapEntry(m *yaml.Node, key string) {
	if m == nil || m.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

// RewriteImageDigest returns the compose YAML with `services.<service>.image`
// replaced by `<repo>@<digest>`, where `<repo>` is the existing image stripped
// of any `:tag` or `@digest` suffix. Used by the rollback path of
// AutoUpdateWorker (task 3.7) to pin a single service to a previous digest
// before a no-pull redeploy. Other services and other fields are preserved.
//
// Errors:
//   - empty digest (caller must filter empty previous digests with
//     rollback_no_previous_digest before calling this helper)
//   - service is not present in the compose
//   - service has no `image:` key
//   - YAML is not valid or has no top-level `services:` mapping
func RewriteImageDigest(composeYAML, service, digest string) (string, error) {
	if service == "" {
		return "", fmt.Errorf("RewriteImageDigest: empty service name")
	}
	if digest == "" {
		return "", fmt.Errorf("RewriteImageDigest: empty digest for service %q", service)
	}
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(composeYAML), &root); err != nil {
		return "", fmt.Errorf("invalid compose YAML: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return "", fmt.Errorf("empty compose YAML")
	}
	rootMap := root.Content[0]
	if rootMap.Kind != yaml.MappingNode {
		return "", fmt.Errorf("compose root is not a mapping")
	}
	servicesNode := findYAMLMapValue(rootMap, "services")
	if servicesNode == nil || servicesNode.Kind != yaml.MappingNode {
		return "", fmt.Errorf("no services defined in compose file")
	}
	svcNode := findYAMLMapValue(servicesNode, service)
	if svcNode == nil {
		return "", fmt.Errorf("RewriteImageDigest: service %q not found in compose", service)
	}
	if svcNode.Kind != yaml.MappingNode {
		return "", fmt.Errorf("RewriteImageDigest: service %q is not a mapping", service)
	}
	imageNode := findYAMLMapValue(svcNode, "image")
	if imageNode == nil {
		return "", fmt.Errorf("RewriteImageDigest: service %q has no image", service)
	}
	if imageNode.Kind != yaml.ScalarNode {
		return "", fmt.Errorf("RewriteImageDigest: service %q image is not a scalar", service)
	}
	repo := splitImageRepo(imageNode.Value)
	if repo == "" {
		return "", fmt.Errorf("RewriteImageDigest: service %q has empty image", service)
	}
	imageNode.Tag = "!!str"
	imageNode.Value = repo + "@" + digest
	// Force a plain (unquoted) style — image refs never need quoting.
	imageNode.Style = 0

	out, err := yaml.Marshal(&root)
	if err != nil {
		return "", fmt.Errorf("failed to marshal compose YAML: %w", err)
	}
	return string(out), nil
}
