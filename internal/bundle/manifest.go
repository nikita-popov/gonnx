// Package bundle — manifest types.
// Manifest schema version: onnxd/v1alpha1
package bundle

// Manifest is the parsed representation of manifest.yaml.
type Manifest struct {
	APIVersion string  `yaml:"apiVersion"`
	Kind       string  `yaml:"kind"`
	Name       string  `yaml:"name"`
	Version    string  `yaml:"version"`
	Source     Source  `yaml:"source"`
	Runtime    Runtime `yaml:"runtime"`
	Handler    Handler `yaml:"handler"`
	Interface  Iface   `yaml:"interface"`
	Policy     Policy  `yaml:"policy"`
	Security   Sec     `yaml:"security"`
}

type Source struct {
	Repo string `yaml:"repo"`
	Ref  string `yaml:"ref"`
	Dir  string `yaml:"dir"`
}

type Runtime struct {
	Engine    string   `yaml:"engine"`
	Model     string   `yaml:"model"`
	Providers []string `yaml:"providers"`
}

type Handler struct {
	Type       string `yaml:"type"`
	Entrypoint string `yaml:"entrypoint"`
	Callable   string `yaml:"callable"`
}

type Iface struct {
	InputSchema  map[string]any `yaml:"inputSchema"`
	OutputSchema map[string]any `yaml:"outputSchema"`
}

type Policy struct {
	StartupTimeoutMs  int    `yaml:"startupTimeoutMs"`
	PredictTimeoutMs  int    `yaml:"predictTimeoutMs"`
	MaxConcurrency    int    `yaml:"maxConcurrency"`
	IdleUnloadSeconds int    `yaml:"idleUnloadSeconds"`
	Network           string `yaml:"network"`
}

type Sec struct {
	AllowUnsigned bool     `yaml:"allowUnsigned"`
	AllowedHosts  []string `yaml:"allowedHosts"`
}
