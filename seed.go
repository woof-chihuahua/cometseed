package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	cfg "github.com/cometbft/cometbft/config"
	"github.com/cometbft/cometbft/libs/log"
	"github.com/cometbft/cometbft/p2p"
	"github.com/cometbft/cometbft/p2p/pex"
	"github.com/cometbft/cometbft/version"
)

// Seed is one network served by cometseed: its own node key, address book,
// listener and PEX reactor in seed mode.
type Seed struct {
	net      *Network
	registry RegistryConfig
	logger   log.Logger

	nodeKey   *p2p.NodeKey
	book      pex.AddrBook
	sw        *p2p.Switch
	transport *p2p.MultiplexTransport

	mu            sync.Mutex
	lastBootstrap time.Time
	lastAdded     int
	lastErrors    []string
	stop          chan struct{}
}

// SeedStatus is what /status reports for a network.
type SeedStatus struct {
	Name            string    `json:"name"`
	ChainID         string    `json:"chain_id"`
	NodeID          string    `json:"node_id"`
	ListenAddress   string    `json:"laddr"`
	ExternalAddress string    `json:"external_address,omitempty"`
	AddrBook        int       `json:"addrbook"`
	Outbound        int       `json:"outbound"`
	Inbound         int       `json:"inbound"`
	Dialing         int       `json:"dialing"`
	LastBootstrap   time.Time `json:"last_bootstrap"`
	LastAdded       int       `json:"last_bootstrap_added"`
	LastErrors      []string  `json:"last_bootstrap_errors,omitempty"`
}

func NewSeed(n *Network, registry RegistryConfig, logger log.Logger) (*Seed, error) {
	s := &Seed{net: n, registry: registry, logger: logger.With("network", n.Name), stop: make(chan struct{})}

	if n.ChainRegistry != "" {
		rc, err := fetchRegistryChain(registry.URL, n.ChainRegistry, registry.Timeout.Duration)
		switch {
		case err != nil && n.ChainID == "":
			return nil, fmt.Errorf("network %q: %w (set chain_id to start without the registry)", n.Name, err)
		case err != nil:
			s.logger.Error("chain registry unavailable, continuing with chain_id", "err", err)
		case n.ChainID == "":
			n.ChainID = rc.ChainID
		case n.ChainID != rc.ChainID:
			return nil, fmt.Errorf("network %q: chain_id %q does not match the registry entry %q (%s)", n.Name, n.ChainID, n.ChainRegistry, rc.ChainID)
		}
	}

	for _, f := range []string{n.NodeKeyFile, n.AddrBookFile} {
		if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
			return nil, err
		}
	}
	nodeKey, err := p2p.LoadOrGenNodeKey(n.NodeKeyFile)
	if err != nil {
		return nil, fmt.Errorf("network %q: node key: %w", n.Name, err)
	}
	s.nodeKey = nodeKey
	return s, nil
}

func (s *Seed) NodeID() p2p.ID { return s.nodeKey.ID() }

func (s *Seed) Start() error {
	n := s.net
	p2pCfg := cfg.DefaultP2PConfig()
	p2pCfg.RootDir = filepath.Dir(n.AddrBookFile)
	p2pCfg.AddrBook = n.AddrBookFile
	p2pCfg.ListenAddress = n.ListenAddress
	p2pCfg.ExternalAddress = n.ExternalAddress
	p2pCfg.SeedMode = true
	p2pCfg.MaxNumInboundPeers = n.MaxInbound
	p2pCfg.MaxNumOutboundPeers = n.MaxOutbound
	p2pCfg.AllowDuplicateIP = n.AllowDuplicateIP
	p2pCfg.AddrBookStrict = n.AddrBookStrict
	p2pCfg.SendRate = n.SendRate
	p2pCfg.RecvRate = n.RecvRate

	listenAddr := n.ExternalAddress
	if listenAddr == "" {
		listenAddr = n.ListenAddress
	}
	nodeInfo := p2p.DefaultNodeInfo{
		ProtocolVersion: p2p.NewProtocolVersion(n.P2PVersion, n.BlockVersion, n.AppVersion),
		DefaultNodeID:   s.nodeKey.ID(),
		ListenAddr:      listenAddr,
		Network:         n.ChainID,
		Version:         version.TMCoreSemVer,
		Channels:        []byte{pex.PexChannel},
		Moniker:         n.Moniker,
		Other:           p2p.DefaultNodeInfoOther{TxIndex: "off"},
	}
	if n.ExternalAddress != "" {
		if _, err := p2p.NewNetAddressString(p2p.IDAddressString(s.nodeKey.ID(), n.ExternalAddress)); err != nil {
			return fmt.Errorf("network %q: external_address %q must be a reachable host:port that resolves: %w", n.Name, n.ExternalAddress, err)
		}
	}
	if err := nodeInfo.Validate(); err != nil {
		return fmt.Errorf("network %q: node info: %w", n.Name, err)
	}

	s.transport = p2p.NewMultiplexTransport(nodeInfo, *s.nodeKey, p2p.MConnConfig(p2pCfg))
	p2p.MultiplexTransportMaxIncomingConnections(n.MaxInbound)(s.transport)

	s.sw = p2p.NewSwitch(p2pCfg, s.transport)
	s.sw.SetLogger(s.logger.With("module", "p2p"))
	s.sw.SetNodeInfo(nodeInfo)
	s.sw.SetNodeKey(s.nodeKey)

	s.book = pex.NewAddrBook(n.AddrBookFile, n.AddrBookStrict)
	s.book.SetLogger(s.logger.With("module", "addrbook"))
	for _, a := range []string{n.ExternalAddress, n.ListenAddress} {
		if a == "" {
			continue
		}
		if addr, err := p2p.NewNetAddressString(p2p.IDAddressString(s.nodeKey.ID(), a)); err == nil {
			s.book.AddOurAddress(addr)
		}
	}
	s.sw.SetAddrBook(s.book)

	reactor := pex.NewReactor(s.book, &pex.ReactorConfig{
		Seeds:                    n.Seeds,
		SeedMode:                 true,
		SeedDisconnectWaitPeriod: n.SeedDisconnectWait,
	})
	reactor.SetLogger(s.logger.With("module", "pex"))
	s.sw.AddReactor("PEX", reactor)

	if err := s.book.Start(); err != nil {
		return err
	}
	s.bootstrap()

	addr, err := p2p.NewNetAddressString(p2p.IDAddressString(s.nodeKey.ID(), n.ListenAddress))
	if err != nil {
		return fmt.Errorf("network %q: laddr: %w", n.Name, err)
	}
	if err := s.transport.Listen(*addr); err != nil {
		return fmt.Errorf("network %q: %w", n.Name, err)
	}
	if err := s.sw.Start(); err != nil {
		return err
	}
	s.logger.Info("seed started", "chain_id", n.ChainID, "id", s.nodeKey.ID(), "laddr", n.ListenAddress,
		"external", n.ExternalAddress, "addrbook", s.book.Size())

	if n.BootstrapInterval > 0 {
		go s.loop()
	}
	return nil
}

func (s *Seed) Stop() {
	close(s.stop)
	if s.sw != nil {
		_ = s.sw.Stop()
	}
	if s.book != nil {
		s.book.Save()
		_ = s.book.Stop()
	}
}

func (s *Seed) loop() {
	t := time.NewTicker(s.net.BootstrapInterval)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			s.bootstrap()
			out, in, dialing := s.sw.NumPeers()
			s.logger.Info("status", "addrbook", s.book.Size(), "outbound", out, "inbound", in, "dialing", dialing)
		}
	}
}

// bootstrap adds candidate peers to the address book from the configured
// peers, the bootstrap RPCs and the chain registry. The PEX crawler takes
// it from there.
func (s *Seed) bootstrap() {
	n := s.net
	var addrs, errs []string
	addrs = append(addrs, n.Peers...)

	rpcs := append([]string{}, n.BootstrapRPC...)
	if n.ChainRegistry != "" && (n.RegistrySeeds || n.RegistryPeers || n.RegistryRPC) {
		rc, err := fetchRegistryChain(s.registry.URL, n.ChainRegistry, s.registry.Timeout.Duration)
		if err != nil {
			errs = append(errs, err.Error())
		} else {
			if n.RegistrySeeds {
				for _, p := range rc.Peers.Seeds {
					addrs = append(addrs, p.String())
				}
			}
			if n.RegistryPeers {
				for _, p := range rc.Peers.PersistentPeers {
					addrs = append(addrs, p.String())
				}
			}
			if n.RegistryRPC {
				for i, r := range rc.APIs.RPC {
					if i >= n.RegistryMaxRPC {
						break
					}
					rpcs = append(rpcs, r.Address)
				}
			}
		}
	}
	for _, rpc := range rpcs {
		peers, err := netInfoPeers(rpc, n.ChainID, n.BootstrapRPCTimeout)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", rpc, err))
			continue
		}
		addrs = append(addrs, peers...)
	}

	added := 0
	seen := map[string]bool{}
	for _, a := range addrs {
		if seen[a] {
			continue
		}
		seen[a] = true
		na, err := p2p.NewNetAddressString(a)
		if err != nil {
			s.logger.Debug("skipping bootstrap address", "addr", a, "err", err)
			continue
		}
		if err := s.book.AddAddress(na, na); err != nil {
			s.logger.Debug("skipping bootstrap address", "addr", a, "err", err)
			continue
		}
		added++
	}
	for _, e := range errs {
		s.logger.Error("bootstrap source failed", "err", e)
	}
	s.logger.Info("bootstrap", "candidates", len(seen), "added", added, "addrbook", s.book.Size())

	s.mu.Lock()
	s.lastBootstrap = time.Now().UTC()
	s.lastAdded = added
	s.lastErrors = errs
	s.mu.Unlock()
}

func (s *Seed) Status() SeedStatus {
	st := SeedStatus{
		Name:            s.net.Name,
		ChainID:         s.net.ChainID,
		NodeID:          string(s.nodeKey.ID()),
		ListenAddress:   s.net.ListenAddress,
		ExternalAddress: s.net.ExternalAddress,
	}
	if s.book != nil {
		st.AddrBook = s.book.Size()
	}
	if s.sw != nil {
		st.Outbound, st.Inbound, st.Dialing = s.sw.NumPeers()
	}
	s.mu.Lock()
	st.LastBootstrap = s.lastBootstrap
	st.LastAdded = s.lastAdded
	st.LastErrors = s.lastErrors
	s.mu.Unlock()
	return st
}
