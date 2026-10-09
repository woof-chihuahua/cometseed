// cometseed is a stateless seed node for CometBFT networks. It speaks only the
// p2p PEX protocol: it crawls each configured network to fill an address book
// and hands peer addresses to the nodes that connect to it. It needs no chain
// binary, no blocks and no application state, and serves any number of
// networks from one process, each on its own port.
package main

import (
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	cmtflags "github.com/cometbft/cometbft/libs/cli/flags"
	"github.com/cometbft/cometbft/libs/log"
	"github.com/cometbft/cometbft/version"
)

var buildVersion = "dev"

//go:embed config.example.toml
var exampleConfig string

const usage = `cometseed - stateless seed node for CometBFT networks

Usage:
  cometseed [-config FILE] [command]

Commands:
  start            run the seed nodes (default)
  node-ids         print the seed address of every enabled network
  check            validate the configuration and the chain registry entries
  example-config   print a configuration with every option documented
  service ACTION   install, update or remove the systemd unit (see: cometseed service)
  version          print the version

Flags:
`

func main() {
	fs := flag.NewFlagSet("cometseed", flag.ExitOnError)
	configPath := fs.String("config", envOr("COMETSEED_CONFIG", "config.toml"), "configuration file (env COMETSEED_CONFIG)")
	noBanner := fs.Bool("no-banner", false, "do not print the comet at startup")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, usage)
		fs.PrintDefaults()
	}
	_ = fs.Parse(os.Args[1:])

	cmd := "start"
	if fs.NArg() > 0 {
		cmd = fs.Arg(0)
		// global flags are also accepted after the command (cometseed version -no-banner),
		// except for service, which has flags of its own
		if cmd != "service" {
			_ = fs.Parse(fs.Args()[1:])
		}
	}
	var err error
	switch cmd {
	case "start":
		err = start(*configPath, !*noBanner)
	case "node-ids":
		err = nodeIDs(*configPath)
	case "check":
		err = check(*configPath)
	case "service":
		err = serviceCmd(*configPath, fs.Args()[1:])
	case "example-config":
		fmt.Print(exampleConfig)
	case "version":
		if !*noBanner {
			printBanner(os.Stdout, isTerminal(os.Stdout))
		}
		fmt.Printf("cometseed %s (cometbft %s, default p2p %d, block %d)\n", buildVersion, version.TMCoreSemVer, defaultP2PVersion, defaultBlockVersion)
	default:
		fs.Usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "cometseed:", err)
		os.Exit(1)
	}
}

func load(path string) (*Config, []*Seed, error) {
	c, err := LoadConfig(path)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	logger, err := newLogger(c.LogLevel, c.LogFormat)
	if err != nil {
		return nil, nil, err
	}
	networks, err := c.ResolveNetworks()
	if err != nil {
		return nil, nil, err
	}
	var seeds []*Seed
	for _, n := range networks {
		s, err := NewSeed(n, c.Registry, logger)
		if err != nil {
			return nil, nil, err
		}
		seeds = append(seeds, s)
	}
	return c, seeds, nil
}

func start(path string, banner bool) error {
	c, seeds, err := load(path)
	if err != nil {
		return err
	}
	if banner && c.LogFormat != "json" {
		printBanner(os.Stdout, isTerminal(os.Stdout))
	}
	logger, _ := newLogger(c.LogLevel, c.LogFormat)
	logger.Info("cometseed starting", "version", buildVersion, "networks", len(seeds), "home", c.Home)

	var started []*Seed
	stopAll := func() {
		for _, s := range started {
			s.Stop()
		}
	}
	for _, s := range seeds {
		if err := s.Start(); err != nil {
			stopAll()
			return err
		}
		started = append(started, s)
	}

	var srv *http.Server
	if c.HTTP.Listen != "" {
		srv = serveHTTP(c.HTTP.Listen, seeds)
		go func() {
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("http server", "err", err)
			}
		}()
		logger.Info("http server", "listen", c.HTTP.Listen)
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs
	logger.Info("stopping")
	if srv != nil {
		_ = srv.Close()
	}
	stopAll()
	return nil
}

func nodeIDs(path string) error {
	_, seeds, err := load(path)
	if err != nil {
		return err
	}
	for _, s := range seeds {
		addr := s.net.ExternalAddress
		if addr == "" {
			addr = s.net.ListenAddress + " (no external_address)"
		}
		fmt.Printf("%-20s %-20s %s@%s\n", s.net.Name, s.net.ChainID, s.NodeID(), addr)
	}
	return nil
}

func check(path string) error {
	_, seeds, err := load(path)
	if err != nil {
		return err
	}
	for _, s := range seeds {
		n := s.net
		fmt.Printf("%s: chain_id=%s laddr=%s external=%q block=%d p2p=%d registry=%q peers=%d seeds=%d rpc=%d\n",
			n.Name, n.ChainID, n.ListenAddress, n.ExternalAddress, n.BlockVersion, n.P2PVersion,
			n.ChainRegistry, len(n.Peers), len(n.Seeds), len(n.BootstrapRPC))
	}
	fmt.Println("configuration OK")
	return nil
}

func newLogger(level, format string) (log.Logger, error) {
	var logger log.Logger
	if format == "json" {
		logger = log.NewTMJSONLogger(log.NewSyncWriter(os.Stdout))
	} else {
		logger = log.NewTMLogger(log.NewSyncWriter(os.Stdout))
	}
	return cmtflags.ParseLogLevel(level, logger, "info")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
