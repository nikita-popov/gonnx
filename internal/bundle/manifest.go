package bundle

// APIVersion is the only supported manifest version.
const APIVersion = "onnxd/v1alpha1"

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

// Source describes where the bundle was fetched from.
type Source struct {
	Repo string `yaml:"repo"`
	Ref  string `yaml:"ref"`
	Dir  string `yaml:"dir"`
}

// Runtime describes how ONNX Runtime should load the model.
type Runtime struct {
	Engine    string         `yaml:"engine"`
	Model     string         `yaml:"model"`
	Providers []string       `yaml:"providers"`
	Session   SessionOptions `yaml:"session"`
}

// SessionOptions are forwarded to ONNX Runtime SessionOptions.
type SessionOptions struct {
	InterOpThreads         int    `yaml:"interOpThreads"`
	IntraOpThreads         int    `yaml:"intraOpThreads"`
	GraphOptimizationLevel string `yaml:"graphOptimizationLevel"`
}

// Handler describes the worker implementation.
type Handler struct {
	Type       string        `yaml:"type"`
	Entrypoint string        `yaml:"entrypoint"`
	Callable   string        `yaml:"callable"`
	Python     PythonOptions `yaml:"python"`
}

// PythonOptions are used when Handler.Type == "python".
type PythonOptions struct {
	Requirements string `yaml:"requirements"`
}

// Iface declares input and output JSON Schemas.
type Iface struct {
	InputSchema  map[string]any `yaml:"inputSchema"`
	OutputSchema map[string]any `yaml:"outputSchema"`
}

// Policy declares operational limits.
type Policy struct {
	StartupTimeoutMs  int              `yaml:"startupTimeoutMs"`
	PredictTimeoutMs  int              `yaml:"predictTimeoutMs"`
	MaxConcurrency    int              `yaml:"maxConcurrency"`
	IdleUnloadSeconds int              `yaml:"idleUnloadSeconds"`
	Network           string           `yaml:"network"`
	Filesystem        FilesystemPolicy `yaml:"filesystem"`
}

// FilesystemPolicy restricts worker filesystem access.
type FilesystemPolicy struct {
	Mode          string   `yaml:"mode"`
	WritablePaths []string `yaml:"writablePaths"`
}

// Sec declares security settings.
type Sec struct {
	AllowUnsigned bool     `yaml:"allowUnsigned"`
	AllowedHosts  []string `yaml:"allowedHosts"`
}
