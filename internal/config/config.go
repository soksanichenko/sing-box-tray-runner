//go:build windows

package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/zelgray/sing-box-tray/assets"
)

const trayConfigFile = "tray-config.json"

type TrayConfig struct {
	SingBoxPath        string               `json:"sing_box_path"`
	WintunDllPath      string               `json:"wintun_dll_path"`
	ConfigDir          string               `json:"config_dir"`
	SelectedConfig     string               `json:"selected_config"`
	SystemProxyInbound string               `json:"system_proxy_inbound"`
	Autostart          bool                 `json:"autostart"`
	StartOnLaunch      bool                 `json:"start_on_launch"`
	DefaultMode        string               `json:"default_mode"`
	LogLines           int                  `json:"log_lines"`
	Language           string               `json:"language"`
	SystemProxy        SystemProxyConfig    `json:"system_proxy"`
	Update             UpdateConfig         `json:"update"`
	LauncherUpdate     LauncherUpdateConfig `json:"launcher_update"`
	TUN                TUNConfig            `json:"tun"`
}

// UpdateConfig controls the sing-box binary auto-updater.
type UpdateConfig struct {
	Channel    string `json:"channel"`     // "stable" or "alpha"
	AutoUpdate bool   `json:"auto_update"` // silently install without prompting
}

// LauncherUpdateConfig controls the tray launcher's own self-updater.
type LauncherUpdateConfig struct {
	AutoUpdate bool `json:"auto_update"` // silently self-update without prompting
}

// SystemProxyConfig describes the default mixed inbound to inject into the
// sing-box config when system-proxy mode is selected and the base config has
// no http/mixed inbound of its own.
type SystemProxyConfig struct {
	Tag        string `json:"tag"`
	Listen     string `json:"listen"`
	ListenPort int    `json:"listen_port"`
}

type TUNConfig struct {
	InterfaceName string   `json:"interface_name"`
	Address       []string `json:"address"`
	MTU           int      `json:"mtu"`
}

func Load(exeDir string) (*TrayConfig, error) {
	path := filepath.Join(exeDir, trayConfigFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if writeErr := os.WriteFile(path, assets.DefaultTrayConfig, 0644); writeErr != nil {
			return nil, fmt.Errorf("write default tray-config.json: %w", writeErr)
		}
		data = assets.DefaultTrayConfig
	} else if err != nil {
		return nil, fmt.Errorf("read tray-config.json: %w", err)
	}
	var cfg TrayConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse tray-config.json: %w", err)
	}
	if cfg.LogLines <= 0 {
		cfg.LogLines = 200
	}
	if cfg.DefaultMode == "" {
		cfg.DefaultMode = "off"
	}
	if cfg.Language == "" {
		cfg.Language = "auto"
	}
	if cfg.Update.Channel == "" {
		cfg.Update.Channel = "stable"
	}
	if cfg.ConfigDir == "" {
		cfg.ConfigDir = "."
	}
	if cfg.SelectedConfig == "" {
		cfg.SelectedConfig = "config.json"
	}
	// Resolve relative paths against the exe directory so exec.Command
	// receives an absolute path (required since Go 1.19).
	cfg.SingBoxPath = absPath(exeDir, cfg.SingBoxPath)
	cfg.WintunDllPath = absPath(exeDir, cfg.WintunDllPath)
	cfg.ConfigDir = absPath(exeDir, cfg.ConfigDir)
	return &cfg, nil
}

// ActiveConfigPath returns the full path to the currently selected sing-box
// config file inside ConfigDir.
func (c *TrayConfig) ActiveConfigPath() string {
	return filepath.Join(c.ConfigDir, c.SelectedConfig)
}

// ListConfigFiles returns the base names of every *.json file directly inside
// dir (non-recursive), sorted alphabetically. tray-config.json is excluded
// since it can live in the same directory but isn't a sing-box config.
func ListConfigFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read config dir: %w", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == trayConfigFile || filepath.Ext(name) != ".json" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// absPath returns p as-is if it is already absolute, otherwise joins it
// with base. Empty strings are passed through unchanged.
func absPath(base, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(base, p)
}

func (c *TrayConfig) Save(exeDir string) error {
	path := filepath.Join(exeDir, trayConfigFile)
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal tray-config.json: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// singBoxConfig is a partial representation of sing-box's config.json,
// used only to locate the proxy inbound address.
type singBoxConfig struct {
	Inbounds []singBoxInbound `json:"inbounds"`
}

type singBoxInbound struct {
	Type       string `json:"type"`
	Tag        string `json:"tag"`
	Listen     string `json:"listen"`
	ListenPort int    `json:"listen_port"`
}

// FindInboundAddr parses the sing-box config at path and returns the listen
// host and port for the inbound matching tag. If tag is empty, returns the
// first http or mixed inbound.
func FindInboundAddr(sbConfigPath, tag string) (host string, port int, err error) {
	data, err := os.ReadFile(sbConfigPath)
	if err != nil {
		return "", 0, fmt.Errorf("read sing-box config: %w", err)
	}
	var cfg singBoxConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", 0, fmt.Errorf("parse sing-box config: %w", err)
	}
	for _, ib := range cfg.Inbounds {
		if ib.Type != "http" && ib.Type != "mixed" {
			continue
		}
		if tag == "" || ib.Tag == tag {
			h := ib.Listen
			if h == "" {
				h = "127.0.0.1"
			}
			return h, ib.ListenPort, nil
		}
	}
	return "", 0, fmt.Errorf("no http/mixed inbound found (tag=%q)", tag)
}

// LoadRawSingBoxConfig reads the sing-box config at path as a generic map,
// preserving all fields so it can be selectively modified and re-marshaled.
func LoadRawSingBoxConfig(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read sing-box config: %w", err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse sing-box config: %w", err)
	}
	return root, nil
}

// WriteRawSingBoxConfig marshals root to a temp file and returns its path.
func WriteRawSingBoxConfig(root map[string]json.RawMessage) (string, error) {
	merged, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal merged config: %w", err)
	}
	tmp, err := os.CreateTemp("", "sing-box-tray-*.json")
	if err != nil {
		return "", fmt.Errorf("create temp config: %w", err)
	}
	defer tmp.Close()
	if _, err := tmp.Write(merged); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("write temp config: %w", err)
	}
	return tmp.Name(), nil
}

// FilterInbounds parses the inbounds JSON array and returns only the entries
// whose "type" is one of keepTypes.
func FilterInbounds(raw json.RawMessage, keepTypes ...string) ([]json.RawMessage, error) {
	keep := make(map[string]bool, len(keepTypes))
	for _, t := range keepTypes {
		keep[t] = true
	}
	if raw == nil {
		return []json.RawMessage{}, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("parse inbounds: %w", err)
	}
	out := items[:0]
	for _, item := range items {
		var t struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(item, &t)
		if keep[t.Type] {
			out = append(out, item)
		}
	}
	return out, nil
}

// InjectSystemProxy reads the sing-box config at sbConfigPath and strips any
// inbound that is not http/mixed (so a TUN inbound left over from the base
// config isn't run alongside the system proxy). If the base config already
// has an http/mixed inbound, it is kept as-is; otherwise a default mixed
// inbound built from cfg is appended. Writes the result to a temp file and
// returns its path.
func InjectSystemProxy(sbConfigPath string, cfg SystemProxyConfig) (string, error) {
	root, err := LoadRawSingBoxConfig(sbConfigPath)
	if err != nil {
		return "", err
	}

	inbounds, err := FilterInbounds(root["inbounds"], "http", "mixed")
	if err != nil {
		return "", err
	}

	if len(inbounds) == 0 {
		defaultRaw, err := json.Marshal(buildDefaultProxyInbound(cfg))
		if err != nil {
			return "", fmt.Errorf("marshal default proxy inbound: %w", err)
		}
		inbounds = append(inbounds, json.RawMessage(defaultRaw))
	}

	inboundsRaw, err := json.Marshal(inbounds)
	if err != nil {
		return "", fmt.Errorf("marshal inbounds: %w", err)
	}
	root["inbounds"] = inboundsRaw

	return WriteRawSingBoxConfig(root)
}

func buildDefaultProxyInbound(cfg SystemProxyConfig) map[string]any {
	tag := cfg.Tag
	if tag == "" {
		tag = "mixed-in"
	}
	listen := cfg.Listen
	if listen == "" {
		listen = "127.0.0.1"
	}
	port := cfg.ListenPort
	if port == 0 {
		port = 2080
	}
	return map[string]any{
		"type":        "mixed",
		"tag":         tag,
		"listen":      listen,
		"listen_port": port,
	}
}
