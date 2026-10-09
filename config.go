package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Config is the whole configuration file.
type Config struct {
	Home      string          `toml:"home"`
	LogLevel  string          `toml:"log_level"`
	LogFormat string          `toml:"log_format"`
	HTTP      HTTPConfig      `toml:"http"`
	Registry  RegistryConfig  `toml:"chain_registry"`
	Defaults  NetworkSettings `toml:"defaults"`
	Networks  []NetworkConfig `toml:"network"`
}

// HTTPConfig is the optional status and metrics server.
type HTTPConfig struct {
	Listen string `toml:"listen"`
}

// RegistryConfig points at a cosmos/chain-registry mirror.
type RegistryConfig struct {
	URL     string   `toml:"url"`
	Timeout Duration `toml:"timeout"`
}

// NetworkSettings holds the tunables that [defaults] sets for every network
// and that each [[network]] can override. Nil means "not set".
type NetworkSettings struct {
	MaxInbound          *int      `toml:"max_inbound"`
	MaxOutbound         *int      `toml:"max_outbound"`
	BootstrapInterval   *Duration `toml:"bootstrap_interval"`
	AddrBookStrict      *bool     `toml:"addr_book_strict"`
	AllowDuplicateIP    *bool     `toml:"allow_duplicate_ip"`
	SendRate            *int64    `toml:"send_rate"`
	RecvRate            *int64    `toml:"recv_rate"`
	SeedDisconnectWait  *Duration `toml:"seed_disconnect_wait"`
	RegistrySeeds       *bool     `toml:"registry_seeds"`
	RegistryPeers       *bool     `toml:"registry_peers"`
	RegistryRPC         *bool     `toml:"registry_rpc"`
	RegistryMaxRPC      *int      `toml:"registry_max_rpc"`
	BootstrapRPCTimeout *Duration `toml:"bootstrap_rpc_timeout"`
}

// NetworkConfig is one [[network]] entry.
type NetworkConfig struct {
	Name            string   `toml:"name"`
	Enabled         *bool    `toml:"enabled"`
	ChainID         string   `toml:"chain_id"`
	ChainRegistry   string   `toml:"chain_registry"`
	ListenAddress   string   `toml:"laddr"`
	ExternalAddress string   `toml:"external_address"`
	Moniker         string   `toml:"moniker"`
	NodeKeyFile     string   `toml:"node_key_file"`
	AddrBookFile    string   `toml:"addr_book_file"`
	BlockVersion    *uint64  `toml:"block_version"`
	P2PVersion      *uint64  `toml:"p2p_version"`
	AppVersion      *uint64  `toml:"app_version"`
	Seeds           []string `toml:"seeds"`
	Peers           []string `toml:"peers"`
	BootstrapRPC    []string `toml:"bootstrap_rpc"`
	NetworkSettings
}

// Duration decodes TOML strings such as "30m" or "10s".
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// Network is a fully resolved network: defaults applied, paths absolute.
type Network struct {
	Name                string
	ChainID             string
	ChainRegistry       string
	ListenAddress       string
	ExternalAddress     string
	Moniker             string
	NodeKeyFile         string
	AddrBookFile        string
	BlockVersion        uint64
	P2PVersion          uint64
	AppVersion          uint64
	Seeds               []string
	Peers               []string
	BootstrapRPC        []string
	MaxInbound          int
	MaxOutbound         int
	BootstrapInterval   time.Duration
	AddrBookStrict      bool
	AllowDuplicateIP    bool
	SendRate            int64
	RecvRate            int64
	SeedDisconnectWait  time.Duration
	RegistrySeeds       bool
	RegistryPeers       bool
	RegistryRPC         bool
	RegistryMaxRPC      int
	BootstrapRPCTimeout time.Duration
}

const (
	defaultBlockVersion = 11
	defaultP2PVersion   = 8
	defaultRegistryURL  = "https://raw.githubusercontent.com/cosmos/chain-registry/master"
	// CometBFT logs every p2p connection at info: keep its modules quiet by default.
	defaultLogLevel = "p2p:none,addrbook:error,pex:error,*:info"
)

var builtinDefaults = Network{
	MaxInbound:          1000,
	MaxOutbound:         30,
	BootstrapInterval:   30 * time.Minute,
	AddrBookStrict:      true,
	AllowDuplicateIP:    true,
	SendRate:            5120000,
	RecvRate:            5120000,
	SeedDisconnectWait:  28 * time.Hour,
	RegistrySeeds:       true,
	RegistryPeers:       true,
	RegistryRPC:         true,
	RegistryMaxRPC:      5,
	BootstrapRPCTimeout: 15 * time.Second,
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// LoadConfig reads and validates the configuration file. Unknown keys are
// errors, so typos do not silently fall back to defaults.
func LoadConfig(path string) (*Config, error) {
	var c Config
	md, err := toml.DecodeFile(path, &c)
	if err != nil {
		return nil, err
	}
	if un := md.Undecoded(); len(un) > 0 {
		keys := make([]string, len(un))
		for i, k := range un {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("unknown keys: %s", strings.Join(keys, ", "))
	}
	if c.Home == "" {
		c.Home = defaultHome()
	}
	c.Home = expandHome(c.Home)
	if c.LogLevel == "" {
		c.LogLevel = defaultLogLevel
	}
	if c.LogFormat == "" {
		c.LogFormat = "plain"
	}
	if c.LogFormat != "plain" && c.LogFormat != "json" {
		return nil, fmt.Errorf("log_format must be plain or json, got %q", c.LogFormat)
	}
	if c.Registry.URL == "" {
		c.Registry.URL = defaultRegistryURL
	}
	if c.Registry.Timeout.Duration == 0 {
		c.Registry.Timeout.Duration = 20 * time.Second
	}
	return &c, nil
}

// ResolveNetworks applies defaults to every enabled network and validates
// them. Missing chain ids are filled from the chain registry by the caller
// before validation completes (see resolveFromRegistry).
func (c *Config) ResolveNetworks() ([]*Network, error) {
	if len(c.Networks) == 0 {
		return nil, errors.New("no [[network]] configured")
	}
	var out []*Network
	names := map[string]bool{}
	for i, nc := range c.Networks {
		if nc.Enabled != nil && !*nc.Enabled {
			continue
		}
		n := builtinDefaults
		applySettings(&n, c.Defaults)
		applySettings(&n, nc.NetworkSettings)

		n.Name = nc.Name
		if n.Name == "" {
			n.Name = nc.ChainID
		}
		if n.Name == "" {
			n.Name = nc.ChainRegistry
		}
		if n.Name == "" {
			return nil, fmt.Errorf("network #%d: name, chain_id or chain_registry is required", i+1)
		}
		n.Name = strings.ReplaceAll(n.Name, "/", "-")
		if !nameRe.MatchString(n.Name) {
			return nil, fmt.Errorf("network %q: invalid name", n.Name)
		}
		if names[n.Name] {
			return nil, fmt.Errorf("network %q: duplicate name", n.Name)
		}
		names[n.Name] = true

		n.ChainID = nc.ChainID
		n.ChainRegistry = nc.ChainRegistry
		n.ListenAddress = nc.ListenAddress
		if n.ListenAddress == "" {
			return nil, fmt.Errorf("network %q: laddr is required", n.Name)
		}
		if !strings.Contains(n.ListenAddress, "://") {
			n.ListenAddress = "tcp://" + n.ListenAddress
		}
		n.ExternalAddress = nc.ExternalAddress
		n.Moniker = nc.Moniker
		if n.Moniker == "" {
			n.Moniker = "cometseed"
		}
		dir := filepath.Join(c.Home, n.Name)
		n.NodeKeyFile = pathOr(nc.NodeKeyFile, filepath.Join(dir, "node_key.json"))
		n.AddrBookFile = pathOr(nc.AddrBookFile, filepath.Join(dir, "addrbook.json"))
		n.BlockVersion = uintOr(nc.BlockVersion, defaultBlockVersion)
		n.P2PVersion = uintOr(nc.P2PVersion, defaultP2PVersion)
		n.AppVersion = uintOr(nc.AppVersion, 0)
		n.Seeds = nc.Seeds
		n.Peers = nc.Peers
		n.BootstrapRPC = nc.BootstrapRPC
		if n.ChainID == "" && n.ChainRegistry == "" {
			return nil, fmt.Errorf("network %q: chain_id or chain_registry is required", n.Name)
		}
		out = append(out, &n)
	}
	if len(out) == 0 {
		return nil, errors.New("every network is disabled")
	}
	ports := map[string]string{}
	for _, n := range out {
		if other, ok := ports[n.ListenAddress]; ok {
			return nil, fmt.Errorf("networks %q and %q share laddr %s: each network needs its own port", other, n.Name, n.ListenAddress)
		}
		ports[n.ListenAddress] = n.Name
	}
	return out, nil
}

func applySettings(n *Network, s NetworkSettings) {
	setInt(&n.MaxInbound, s.MaxInbound)
	setInt(&n.MaxOutbound, s.MaxOutbound)
	setDur(&n.BootstrapInterval, s.BootstrapInterval)
	setBool(&n.AddrBookStrict, s.AddrBookStrict)
	setBool(&n.AllowDuplicateIP, s.AllowDuplicateIP)
	if s.SendRate != nil {
		n.SendRate = *s.SendRate
	}
	if s.RecvRate != nil {
		n.RecvRate = *s.RecvRate
	}
	setDur(&n.SeedDisconnectWait, s.SeedDisconnectWait)
	setBool(&n.RegistrySeeds, s.RegistrySeeds)
	setBool(&n.RegistryPeers, s.RegistryPeers)
	setBool(&n.RegistryRPC, s.RegistryRPC)
	setInt(&n.RegistryMaxRPC, s.RegistryMaxRPC)
	setDur(&n.BootstrapRPCTimeout, s.BootstrapRPCTimeout)
}

func setInt(dst *int, v *int) {
	if v != nil {
		*dst = *v
	}
}

func setBool(dst *bool, v *bool) {
	if v != nil {
		*dst = *v
	}
}

func setDur(dst *time.Duration, v *Duration) {
	if v != nil {
		*dst = v.Duration
	}
}

func uintOr(v *uint64, def uint64) uint64 {
	if v != nil {
		return *v
	}
	return def
}

func pathOr(p, def string) string {
	if p == "" {
		return def
	}
	return expandHome(p)
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

func defaultHome() string {
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".cometseed")
	}
	return ".cometseed"
}
