//go:build windows

package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zelgray/sing-box-tray/assets"
)

const trayConfigFile = "tray-config.json"

type TrayConfig struct {
	SingBoxPath        string    `json:"sing_box_path"`
	WintunDllPath      string    `json:"wintun_dll_path"`
	ConfigPath         string    `json:"config_path"`
	SystemProxyInbound string    `json:"system_proxy_inbound"`
	Autostart          bool      `json:"autostart"`
	StartOnLaunch      bool      `json:"start_on_launch"`
	DefaultMode        string    `json:"default_mode"`
	LogLines           int       `json:"log_lines"`
	TUN                TUNConfig `json:"tun"`
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
	// Resolve relative paths against the exe directory so exec.Command
	// receives an absolute path (required since Go 1.19).
	cfg.SingBoxPath = absPath(exeDir, cfg.SingBoxPath)
	cfg.WintunDllPath = absPath(exeDir, cfg.WintunDllPath)
	cfg.ConfigPath = absPath(exeDir, cfg.ConfigPath)
	return &cfg, nil
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
