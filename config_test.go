package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExampleConfigLoads(t *testing.T) {
	c, err := LoadConfig(writeConfig(t, exampleConfig))
	if err != nil {
		t.Fatal(err)
	}
	nets, err := c.ResolveNetworks()
	if err != nil {
		t.Fatal(err)
	}
	if len(nets) != 1 || nets[0].ChainID != "chihuahua-1" || nets[0].ListenAddress != "tcp://0.0.0.0:26666" {
		t.Fatalf("unexpected networks: %+v", nets)
	}
}

func TestDefaultsAndOverrides(t *testing.T) {
	c, err := LoadConfig(writeConfig(t, `
home = "/tmp/cs"
[defaults]
max_inbound = 50
bootstrap_interval = "5m"

[[network]]
chain_id = "a-1"
laddr = "0.0.0.0:26666"

[[network]]
name = "b"
chain_id = "b-1"
laddr = "tcp://0.0.0.0:26667"
max_inbound = 7
block_version = 12
registry_rpc = false

[[network]]
chain_id = "c-1"
laddr = "tcp://0.0.0.0:26668"
enabled = false
`))
	if err != nil {
		t.Fatal(err)
	}
	nets, err := c.ResolveNetworks()
	if err != nil {
		t.Fatal(err)
	}
	if len(nets) != 2 {
		t.Fatalf("want 2 enabled networks, got %d", len(nets))
	}
	a, b := nets[0], nets[1]
	if a.Name != "a-1" || a.ListenAddress != "tcp://0.0.0.0:26666" || a.MaxInbound != 50 || a.BootstrapInterval != 5*time.Minute {
		t.Fatalf("network a: %+v", a)
	}
	if a.BlockVersion != 11 || a.P2PVersion != 8 || a.MaxOutbound != 30 || !a.RegistryRPC {
		t.Fatalf("network a defaults: %+v", a)
	}
	if a.NodeKeyFile != "/tmp/cs/a-1/node_key.json" || a.AddrBookFile != "/tmp/cs/a-1/addrbook.json" {
		t.Fatalf("network a paths: %s %s", a.NodeKeyFile, a.AddrBookFile)
	}
	if b.MaxInbound != 7 || b.BlockVersion != 12 || b.RegistryRPC || b.BootstrapInterval != 5*time.Minute {
		t.Fatalf("network b: %+v", b)
	}
}

func TestConfigErrors(t *testing.T) {
	cases := map[string]string{
		"unknown keys": `
[[network]]
chain_id = "a-1"
laddr = "tcp://0.0.0.0:1"
max_inbund = 3
`,
		"share laddr": `
[[network]]
chain_id = "a-1"
laddr = "tcp://0.0.0.0:1"
[[network]]
chain_id = "b-1"
laddr = "tcp://0.0.0.0:1"
`,
		"duplicate name": `
[[network]]
chain_id = "a-1"
laddr = "tcp://0.0.0.0:1"
[[network]]
chain_id = "a-1"
laddr = "tcp://0.0.0.0:2"
`,
		"laddr is required": `
[[network]]
chain_id = "a-1"
`,
		"no [[network]]": `log_level = "info"`,
		"log_format": `
log_format = "xml"
[[network]]
chain_id = "a-1"
laddr = "tcp://0.0.0.0:1"
`,
	}
	for want, body := range cases {
		c, err := LoadConfig(writeConfig(t, body))
		if err == nil {
			_, err = c.ResolveNetworks()
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got error %v", want, err)
		}
	}
}
