//go:build windows

package tun

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/zelgray/sing-box-tray/internal/config"
)

// InjectTUN reads the sing-box config at sbConfigPath, removes any existing
// tun inbound, appends a new one from cfg, prepends a process-exclusion route
// rule so sing-box's own traffic is not looped back through TUN, writes the
// result to a temp file, and returns its path.
func InjectTUN(sbConfigPath string, cfg config.TUNConfig, singBoxPath string) (string, error) {
	data, err := os.ReadFile(sbConfigPath)
	if err != nil {
		return "", fmt.Errorf("read sing-box config: %w", err)
	}

	// Parse config as a generic map to preserve all fields.
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return "", fmt.Errorf("parse sing-box config: %w", err)
	}

	// Strip any existing tun inbound.
	inbounds, err := filterInbounds(root["inbounds"])
	if err != nil {
		return "", err
	}

	tunInbound := buildTUNInbound(cfg)
	tunRaw, err := json.Marshal(tunInbound)
	if err != nil {
		return "", fmt.Errorf("marshal tun inbound: %w", err)
	}
	inbounds = append(inbounds, json.RawMessage(tunRaw))

	inboundsRaw, err := json.Marshal(inbounds)
	if err != nil {
		return "", fmt.Errorf("marshal inbounds: %w", err)
	}
	root["inbounds"] = json.RawMessage(inboundsRaw)

	// Prepend a route rule that sends sing-box's own process traffic directly,
	// preventing it from looping back through the TUN interface.
	if err := injectSelfBypassRule(root, filepath.Base(singBoxPath)); err != nil {
		return "", err
	}

	merged, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal merged config: %w", err)
	}

	tmp, err := os.CreateTemp("", "sing-box-tun-*.json")
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

// EnsureWintunDll copies wintun.dll from src to dstDir/wintun.dll if the
// destination does not already exist.
func EnsureWintunDll(src, dstDir string) error {
	if src == "" {
		return nil
	}
	dst := filepath.Join(dstDir, "wintun.dll")
	if _, err := os.Stat(dst); err == nil {
		return nil // already present
	}
	return copyFile(src, dst)
}

// filterInbounds parses the inbounds JSON array and removes any tun entries.
func filterInbounds(raw json.RawMessage) ([]json.RawMessage, error) {
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
		if t.Type != "tun" {
			out = append(out, item)
		}
	}
	return out, nil
}

func buildTUNInbound(cfg config.TUNConfig) map[string]any {
	addr := cfg.Address
	if len(addr) == 0 {
		addr = []string{"172.19.0.1/30"}
	}
	mtu := cfg.MTU
	if mtu == 0 {
		mtu = 9000
	}
	name := cfg.InterfaceName
	if name == "" {
		name = "singbox-tun"
	}
	return map[string]any{
		"type":           "tun",
		"tag":            "tun-in",
		"interface_name": name,
		"address":        addr,
		"mtu":            mtu,
		"auto_route":     true,
		"strict_route":   true,
	}
}

// injectSelfBypassRule patches the route section for TUN mode:
//   - sets auto_detect_interface: true so sing-box knows which physical NIC to
//     use when building the auto_route routing table (without this the default
//     routes that redirect browser traffic into TUN are not set up correctly on
//     Windows)
//   - prepends a process_name rule that sends sing-box's own connections via
//     "direct", breaking the TUN loop for the proxy process itself
func injectSelfBypassRule(root map[string]json.RawMessage, processName string) error {
	var route map[string]json.RawMessage
	if raw, ok := root["route"]; ok {
		if err := json.Unmarshal(raw, &route); err != nil {
			return fmt.Errorf("parse route: %w", err)
		}
	} else {
		route = make(map[string]json.RawMessage)
	}

	// auto_detect_interface is required for auto_route to work on Windows: it
	// tells sing-box which physical interface carries the real default gateway,
	// so it can add exclusion routes and properly redirect all other traffic
	// through TUN. Only inject if the user hasn't set it explicitly.
	if _, ok := route["auto_detect_interface"]; !ok {
		trueVal, _ := json.Marshal(true)
		route["auto_detect_interface"] = trueVal
	}

	var rules []json.RawMessage
	if raw, ok := route["rules"]; ok {
		if err := json.Unmarshal(raw, &rules); err != nil {
			return fmt.Errorf("parse route.rules: %w", err)
		}
	}

	bypassRule := map[string]any{
		"process_name": []string{processName},
		"outbound":     "direct",
	}
	bypassRaw, err := json.Marshal(bypassRule)
	if err != nil {
		return fmt.Errorf("marshal bypass rule: %w", err)
	}
	rules = append([]json.RawMessage{json.RawMessage(bypassRaw)}, rules...)

	rulesRaw, err := json.Marshal(rules)
	if err != nil {
		return fmt.Errorf("marshal route.rules: %w", err)
	}
	route["rules"] = rulesRaw

	routeRaw, err := json.Marshal(route)
	if err != nil {
		return fmt.Errorf("marshal route: %w", err)
	}
	root["route"] = routeRaw
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open wintun.dll source: %w", err)
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create wintun.dll destination: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy wintun.dll: %w", err)
	}
	return nil
}
