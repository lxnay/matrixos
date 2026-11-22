package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

type searchPath struct {
	fileName      string
	dirPath       string
	confRoot      string
	artifactsRoot string
	defaultRoot   string
}

// ConfigPath returns the full path to the config file for this search path.
func (sp *searchPath) ConfigPath() string {
	return filepath.Join(sp.dirPath, sp.fileName)
}

const (
	// BaseConfigFileName is the name of the main configuration file that vector looks for.
	BaseConfigFileName = "matrixos.conf"
	// ClientConfigFileName is the name of the client configuration file that vector looks for.
	ClientConfigFileName = "client.conf"
	// MarkerFileName is the name of the marker file that indicates the root of the matrixOS toolkit.
	MarkerFileName = ".matrixos"
)

// smartRootify translates matrixOS.Root into a path that's complying with the config var
// specifications.
func smartRootify(path, defaultRoot string) (string, error) {
	if filepath.IsAbs(path) {
		return path, nil
	}

	// Get the working directory so that we can compare it with path.
	if defaultRoot == "" {
		var err error
		defaultRoot, err = os.Getwd()
		if err != nil {
			return defaultRoot, err
		}
	}

	rootPath := filepath.Join(defaultRoot, path)

	return filepath.Abs(rootPath)
}

func findMarkerDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	goUp := func() bool {
		parent := filepath.Dir(cwd)
		if parent == cwd {
			return false
		}
		cwd = parent
		return true
	}

	for {
		dotMatrixosPath := filepath.Join(cwd, MarkerFileName)
		if _, err := os.Stat(dotMatrixosPath); err != nil {
			if os.IsNotExist(err) {
				if !goUp() {
					break
				}
				continue
			}
			// Error found, and is not "not exist".
			return "", err
		}

		return cwd, nil
	}

	return "", fmt.Errorf("marker path not found in any parent directories of %s", cwd)
}

// SystemSearchPathEnabled controls whether the fallback system-level
// search path (/etc/matrixos/conf) is included when resolving config
// files.  Tests can set this to false to avoid picking up host
// configuration.
var SystemSearchPathEnabled = true

func searchPaths(cfgName string) []searchPath {
	// Navigate CWD up until we find a .matrixos file.
	var sps []searchPath

	markerDir, err := findMarkerDir()
	if err == nil && markerDir != "" {
		sps = append(sps, searchPath{
			fileName:      cfgName,
			dirPath:       filepath.Join(markerDir, "conf"),
			confRoot:      markerDir,
			artifactsRoot: markerDir,
			defaultRoot:   markerDir,
		})
	}

	if SystemSearchPathEnabled {
		// add this as last resort option at the moment.
		sps = append(sps, searchPath{
			// Setup for when vector runs from an installed location,
			// with config in /etc/matrixos/conf.
			fileName:      cfgName,
			dirPath:       "/etc/matrixos/conf",
			confRoot:      "/etc/matrixos",
			artifactsRoot: "/var/cache/matrixos",
			defaultRoot:   "/usr/lib/matrixos",
		})
	}

	return sps
}

// IniConfig is a config reader that loads values from an INI file.
type IniConfig struct {
	mu  sync.RWMutex
	sp  *searchPath
	cfg map[string][]string
}

func cfgNameToSearchPath(cfgName string) *searchPath {
	for _, sp := range searchPaths(cfgName) {
		if _, err := os.Stat(sp.ConfigPath()); err == nil {
			return &sp
		}
	}
	return nil
}

// NewBaseConfig creates a new IniConfig instance for the base configuration.
func NewBaseConfig() (*IniConfig, error) {
	return NewIniConfig(BaseConfigFileName)
}

// NewClientConfig creates a new IniConfig instance for the client configuration.
func NewClientConfig() (*IniConfig, error) {
	return NewIniConfig(ClientConfigFileName)
}

// NewIniConfig creates a new IniConfig instance.
func NewIniConfig(configName string) (*IniConfig, error) {
	sp := cfgNameToSearchPath(configName)
	if sp == nil {
		return nil, fmt.Errorf(
			"config file not found in any of the paths: %v",
			searchPaths(configName),
		)
	}
	return &IniConfig{
		sp: sp,
	}, nil
}

// ConfigFromPathParams holds parameters for creating a config from a specific path.
type ConfigFromPathParams struct {
	ConfigPath    string
	DefaultRoot   string
	ConfRoot      string
	ArtifactsRoot string
}

// NewIniConfigFromPath creates a new IniConfig instance with the specified file path.
func NewIniConfigFromPath(params *ConfigFromPathParams) (*IniConfig, error) {
	if params == nil {
		return nil, fmt.Errorf("params is nil")
	}
	if params.ConfigPath == "" {
		return nil, fmt.Errorf("config path is empty")
	}
	if params.DefaultRoot == "" {
		return nil, fmt.Errorf("default root is empty")
	}
	if params.ConfRoot == "" {
		return nil, fmt.Errorf("conf root is empty")
	}
	if params.ArtifactsRoot == "" {
		return nil, fmt.Errorf("artifacts root is empty")
	}
	sp := searchPath{
		fileName:      filepath.Base(params.ConfigPath),
		dirPath:       filepath.Dir(params.ConfigPath),
		defaultRoot:   params.DefaultRoot,
		confRoot:      params.ConfRoot,
		artifactsRoot: params.ArtifactsRoot,
	}
	return &IniConfig{
		sp: &sp,
	}, nil
}

// Clone creates a deep copy of the IniConfig instance.
// This is useful for forking off configs with overlays,
// without mutating the original config.
func (c *IniConfig) Clone() IConfig {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	var sp searchPath
	if c.sp != nil {
		sp = *(c.sp) // copy.
	}
	clone := &IniConfig{
		sp:  &sp,
		cfg: make(map[string][]string),
	}
	for k, v := range c.cfg {
		clone.cfg[k] = slices.Clone(v)
	}
	return clone
}

// AddOverlay adds the provided overlay to the config.
// The overlay is a map where keys are config keys and values
// are slices of config values.
func (c *IniConfig) AddOverlay(overlay map[string][]string) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	if overlay == nil {
		return fmt.Errorf("overlay is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cfg == nil {
		c.cfg = make(map[string][]string)
	}
	for k, v := range overlay {
		vals, ok := c.cfg[k]
		if !ok {
			c.cfg[k] = slices.Clone(v)
			continue
		}
		vals = append(vals, v...)
		c.cfg[k] = vals
	}
	return nil
}

func (c *IniConfig) loadAndGenerateConfig(configPath string) error {
	ini, err := LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config %s: %w", configPath, err)
	}
	c.generateConfig(ini)
	return nil
}

func (c *IniConfig) generateSubConfigs(configPath string) error {
	// configPath is a valid path to a config file.
	// Use this path to build a list of subconfigs to load, starting
	// with configPath + ".d/*.conf".
	subconfigDir := configPath + ".d"
	subconfigs, err := os.ReadDir(subconfigDir)
	if err != nil {
		// If the directory doesn't exist, that's fine.
		// it just means there are no subconfigs.
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf(
			"failed to read subconfig directory %s: %w",
			subconfigDir,
			err,
		)
	}

	for _, entry := range subconfigs {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".conf" {
			continue
		}
		subconfigPath := filepath.Join(subconfigDir, entry.Name())
		err := c.loadAndGenerateConfig(subconfigPath)
		if err != nil {
			return fmt.Errorf(
				"failed to load subconfig %s: %w",
				subconfigPath,
				err,
			)
		}
	}

	return nil
}

func (c *IniConfig) generateConfig(ini IniFile) {
	if c.cfg == nil {
		c.cfg = make(map[string][]string)
	}

	for section, items := range ini {
		for key, value := range items {
			// Flatten the key: [Section] Key -> Section.Key
			var fullKey string
			if section == "" {
				fullKey = key
			} else {
				fullKey = fmt.Sprintf("%s.%s", section, key)
			}

			val, ok := c.cfg[fullKey]
			if !ok {
				val = []string{}
			}
			val = append(val, value) // preserve history.
			c.cfg[fullKey] = val
		}
	}
}

func (c *IniConfig) generateParent(ini IniFile) error {
	mos, ok := ini["matrixOS"]
	if !ok {
		return nil
	}
	parentVal, ok := mos["ParentConfig"]
	if !ok {
		return nil
	}
	parentPath := filepath.Join(c.sp.dirPath, parentVal)
	if _, err := os.Stat(parentPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	return c.loadAndGenerateConfig(parentPath)
}

type rootConfigSpec struct {
	key        string
	defaultVal string
	rootify    bool
	expandAbs  bool
	dependents []string
}

func (c *IniConfig) loadRootConfig(fullPath string, spec rootConfigSpec) error {
	val, found := c.getVal(spec.key)
	if !found {
		log.Printf(
			`WARNING WARNING WARNING:
- %s is not set in %s.
- Relative paths depending on it will be expanded using %s.
- Those paths are:
  - %s`,
			spec.key,
			fullPath,
			spec.defaultVal,
			strings.Join(spec.dependents, "\n  - "),
		)
		c.setVal(spec.key, spec.defaultVal)
	} else if spec.rootify {
		rootVal, err := smartRootify(val, spec.defaultVal)
		if err != nil {
			return err
		}
		c.setVal(spec.key, rootVal)
	}

	if spec.expandAbs {
		if err := c.expandAbs(spec.key); err != nil {
			return err
		}
	}

	for _, key := range spec.dependents {
		c.expand(key, spec.key)
	}
	return nil
}

func (c *IniConfig) loadPrivateRepoConfigs() {
	privateRepoDependents := []string{
		"Seeder.SecureBootPrivateKey",
		"Seeder.SecureBootPublicKey",
		"Seeder.SecureBootKekPublicKey",
		"Ostree.GpgPrivateKey",
		"Ostree.GpgPublicKey",
	}
	for _, key := range privateRepoDependents {
		c.expand(key, "matrixOS.PrivateGitRepoPath")
	}
	defaultPrivateRepoDependents := []string{
		"Seeder.DefaultSecureBootPrivateKey",
		"Seeder.DefaultSecureBootPublicKey",
	}
	for _, key := range defaultPrivateRepoDependents {
		c.expand(key, "matrixOS.DefaultPrivateGitRepoPath")
	}
}

func (c *IniConfig) Load() error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.sp == nil {
		return fmt.Errorf("no configuration found in any of the search paths.")
	}
	fullPath := c.sp.ConfigPath()
	ini, err := LoadConfig(fullPath)
	if err != nil {
		return err
	}

	// Build into a fresh staging map so reloads do not duplicate history
	// slices and errors never leave partial state in c.cfg.
	staging := &IniConfig{
		sp:  c.sp,
		cfg: make(map[string][]string),
	}

	if err := staging.generateParent(ini); err != nil {
		return err
	}
	staging.generateConfig(ini)
	if err := staging.generateSubConfigs(fullPath); err != nil {
		return err
	}

	rootSpecs := []rootConfigSpec{
		{
			key:        "matrixOS.Root",
			defaultVal: c.sp.defaultRoot,
			rootify:    true,
			expandAbs:  true,
			dependents: []string{
				"matrixOS.PrivateGitRepoPath",
				"matrixOS.LogsDir",
				"matrixOS.LocksDir",
				"Seeder.LocksDir",
				"Releaser.HooksDir",
				"Seeder.SeedersDir",
				"Releaser.LocksDir",
				"Imager.LocksDir",
				"Ostree.RepoDir",
			},
		},
		{
			key:        "matrixOS.DefaultRoot",
			defaultVal: c.sp.defaultRoot,
			dependents: []string{
				"Seeder.ChrootSeedersDir",
			},
		},
		{
			key:        "matrixOS.ConfRoot",
			defaultVal: c.sp.confRoot,
			rootify:    true,
			dependents: []string{
				"Ostree.GpgOfficialPublicKey",
				"Imager.HooksDir",
				"Imager.TestsDir",
			},
		},
		{
			key:        "matrixOS.ArtifactsRoot",
			defaultVal: c.sp.artifactsRoot,
			rootify:    true,
			dependents: []string{
				"Ostree.DevGpgHomeDir",
				"Seeder.BinpkgsDir",
				"Seeder.DownloadsDir",
				"Seeder.DistfilesDir",
				"Seeder.GpgKeysDir",
				"Seeder.PortageReposDir",
				"Imager.ImagesDir",
				"Imager.MountDir",
			},
		},
	}
	for _, spec := range rootSpecs {
		if err := staging.loadRootConfig(fullPath, spec); err != nil {
			return err
		}
	}

	staging.loadPrivateRepoConfigs()
	c.cfg = staging.cfg

	return nil
}

// GetItem retrieves the single config value associated to the provided config key.
// If multiple values are present, it returns the last one.
func (c *IniConfig) GetItem(key string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("config is nil")
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	lst, ok := c.cfg[key]
	if !ok {
		return "", fmt.Errorf("invalid key %s", key)
	}

	var val string
	if len(lst) > 0 {
		val = lst[len(lst)-1]
	}
	return val, nil
}

func (c *IniConfig) GetBool(key string) (bool, error) {
	val, err := c.GetItem(key)
	if err != nil {
		return false, err
	}
	return val == "true", nil
}

func (c *IniConfig) GetItems(key string) ([]string, error) {
	var vals []string
	if c == nil {
		return vals, fmt.Errorf("config is nil")
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	lst, ok := c.cfg[key]
	if !ok {
		return vals, fmt.Errorf("invalid key %s", key)
	}
	return slices.Clone(lst), nil
}

func (c *IniConfig) getVal(key string) (string, bool) {
	if vals, ok := c.cfg[key]; ok && len(vals) > 0 {
		return vals[len(vals)-1], true
	}
	return "", false
}

func (c *IniConfig) setVal(key, val string) {
	if vals, ok := c.cfg[key]; ok && len(vals) > 0 {
		vals[len(vals)-1] = val
		return
	}
	c.cfg[key] = []string{val}
}

func (c *IniConfig) expandAbs(key string) error {
	val, ok := c.getVal(key)
	if !ok {
		return nil
	}
	if !filepath.IsAbs(val) {
		abs, err := filepath.Abs(val)
		if err != nil {
			return fmt.Errorf("failed to resolve absolute path for %s: %w", key, err)
		}
		c.setVal(key, abs)
	}
	return nil
}

func (c *IniConfig) expand(key, baseKey string) {
	val, ok := c.getVal(key)
	if !ok {
		return
	}
	if filepath.IsAbs(val) {
		return
	}
	base, ok := c.getVal(baseKey)
	if !ok {
		return
	}
	c.setVal(key, filepath.Join(base, val))
}
