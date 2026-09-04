package dmctl

import (
	json "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
)

// ErrConfigPermissions is a config file other users can read.
var ErrConfigPermissions = errors.New("dmctl: config file is readable by other users")

// Config is the on-disk configuration.
//
// A context holds a reference to a credential, never the credential.
// micromdm's mdmctl writes the live API token into ~/.micromdm/<name>.json
// under a directory it creates 0777, and its `config print` echoes the token
// to stdout; nanohubctl writes the key and prints it too. The file is the
// thing that leaks, and naming an environment variable or a file path costs
// the operator nothing.
type Config struct {
	Current  string             `json:"current"`
	Contexts map[string]Context `json:"contexts"`
}

// Context is one server the CLI talks to.
type Context struct {
	Server string `json:"server"`
	// TokenEnv names an environment variable holding the credential.
	TokenEnv string `json:"token_env,omitempty"`
	// TokenFile names a file holding the credential.
	TokenFile string `json:"token_file,omitempty"`
	// Token is an inlined credential. Writing one requires an explicit flag
	// and prints a warning; it exists because some environments have nowhere
	// better, not because it is a good idea.
	Token string `json:"token,omitempty"`
}

// token resolves the context's credential.
func (c Context) token(getenv func(string) string) (string, error) {
	switch {
	case c.TokenEnv != "":
		v := strings.TrimSpace(getenv(c.TokenEnv))
		if v == "" {
			return "", fmt.Errorf("dmctl: %s is empty", c.TokenEnv)
		}
		return v, nil
	case c.TokenFile != "":
		raw, err := os.ReadFile(c.TokenFile)
		if err != nil {
			return "", fmt.Errorf("dmctl: read token file: %w", err)
		}
		return strings.TrimSpace(string(raw)), nil
	default:
		return c.Token, nil
	}
}

// DefaultConfigPath is where the config lives when none is given.
func DefaultConfigPath(getenv func(string) string) string {
	if p := getenv(EnvConfig); p != "" {
		return p
	}
	base := getenv("XDG_CONFIG_HOME")
	if base == "" {
		home := getenv("HOME")
		if home == "" {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "go-apple-dm", "dmctl.json")
}

// loadConfig reads the active context, or nil when there is no config file.
func (e *env) loadConfig() (*Context, error) {
	path := e.opts.config
	if path == "" {
		path = DefaultConfigPath(e.getenv)
	}
	if path == "" {
		return nil, nil
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dmctl: config: %w", err)
	}
	// A credential-adjacent file that other users can read is refused rather
	// than used, because reading it is what makes the leak matter.
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		return nil, fmt.Errorf("%w: %s is %#o, want 0600", ErrConfigPermissions, path, mode)
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- an operator's own config path
	if err != nil {
		return nil, fmt.Errorf("dmctl: config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("dmctl: config %s: %w", path, err)
	}
	name := e.opts.context
	if name == "" {
		name = cfg.Current
	}
	if name == "" {
		return nil, nil
	}
	ctx, ok := cfg.Contexts[name]
	if !ok {
		return nil, fmt.Errorf("%w: no context %q in %s", ErrUsage, name, path)
	}
	return &ctx, nil
}

// version reports the module version the binary was built from.
func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return "devel"
	}
	return info.Main.Version
}
