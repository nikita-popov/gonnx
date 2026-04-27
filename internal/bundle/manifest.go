package bundle

// APIVersion is the only supported manifest version.
const APIVersion = "onnxd/v1alpha1"

// Manifest is the parsed representation of manifest.yaml.
type Manifest struct {
	APIVersion string     `yaml:"apiVersion"`
	Kind       string     `yaml:"kind"`
	Name       string     `yaml:"name"`
	Version    string     `yaml:"version"`
	Source     Source     `yaml:"source"`
	Runtime    Runtime    `yaml:"runtime"`
	Handler    Handler    `yaml:"handler"`
	Interface  Iface      `yaml:"interface"`
	Policy     Policy     `yaml:"policy"`
	Security   Sec        `yaml:"security"`
	Assets     []Asset    `yaml:"assets"`
	System     SystemDeps `yaml:"system"`
}

// SystemDeps groups host-level dependency declarations.
type SystemDeps struct {
	Deps []SystemDep `yaml:"deps"`
}

// SystemDep describes a single system-level dependency (binary, shared
// library, etc.) that must be present on the host for the bundle to work.
//
// Example manifest fragment:
//
//	system:
//	  deps:
//	    - name: espeak-ng
//	      check: "espeak-ng --version"
//	      hint: "apt install espeak-ng"
type SystemDep struct {
	// Name is a human-readable label for the dependency.
	Name string `yaml:"name"`

	// Check is a shell command whose exit code indicates presence (0 = ok).
	// Example: "espeak-ng --version"
	Check string `yaml:"check"`

	// Hint is an optional installation suggestion shown in warnings/errors.
	// Example: "apt install espeak-ng"
	Hint string `yaml:"hint"`
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

// Asset declares a large binary file to be fetched during `gonnxctl pull`.
// Asset files are not committed to Git; they are downloaded, sha256-verified,
// and placed at Dest relative to the bundle directory.
type Asset struct {
	// ID is a unique symbolic name within the manifest (snake_case).
	ID string `yaml:"id"`

	// URL is the download location. Supported schemes: https, s3, gs.
	URL string `yaml:"url"`

	// SHA256 is the expected lowercase hex-encoded SHA-256 digest of the file
	// contents after any unpacking. Mandatory. Used as a cache key: if the
	// on-disk file already has this digest, the download is skipped.
	SHA256 string `yaml:"sha256"`

	// Size is the expected file size in bytes, used only for progress reporting.
	Size int64 `yaml:"size,omitempty"`

	// Dest is the destination path relative to the bundle directory.
	// Directory traversal (containing "..") is rejected at validation time.
	Dest string `yaml:"dest"`

	// Auth describes how to obtain credentials for the download.
	// Optional; omit for public URLs.
	Auth *AssetAuth `yaml:"auth,omitempty"`

	// Unpack describes how to extract an archive after download.
	// Optional; omit for plain files.
	Unpack *AssetUnpack `yaml:"unpack,omitempty"`
}

// AssetAuth holds credentials configuration for an asset download.
// The token value is never stored in the manifest; only the env var name is.
type AssetAuth struct {
	// Env is the name of the environment variable whose value is sent
	// as a Bearer token in the Authorization header.
	Env string `yaml:"env"`
}

// AssetUnpack describes how to extract a downloaded archive.
type AssetUnpack struct {
	// Format is the archive format: "tar.gz", "tar.bz2", or "zip".
	Format string `yaml:"format"`

	// Strip is the number of leading path components to strip when extracting,
	// equivalent to tar --strip-components.
	Strip int `yaml:"strip,omitempty"`
}
