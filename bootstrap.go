package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// registryChain is the subset of a chain-registry chain.json that cometseed uses.
type registryChain struct {
	ChainID string `json:"chain_id"`
	Peers   struct {
		Seeds           []registryPeer `json:"seeds"`
		PersistentPeers []registryPeer `json:"persistent_peers"`
	} `json:"peers"`
	APIs struct {
		RPC []struct {
			Address string `json:"address"`
		} `json:"rpc"`
	} `json:"apis"`
}

type registryPeer struct {
	ID      string `json:"id"`
	Address string `json:"address"`
}

func (p registryPeer) String() string { return p.ID + "@" + p.Address }

func fetchRegistryChain(baseURL, name string, timeout time.Duration) (*registryChain, error) {
	url := strings.TrimRight(baseURL, "/") + "/" + strings.Trim(name, "/") + "/chain.json"
	var rc registryChain
	if err := getJSON(url, timeout, &rc); err != nil {
		return nil, fmt.Errorf("chain registry %s: %w", name, err)
	}
	if rc.ChainID == "" {
		return nil, fmt.Errorf("chain registry %s: no chain_id", name)
	}
	return &rc, nil
}

type netInfo struct {
	Result struct {
		Peers []struct {
			NodeInfo struct {
				ID         string `json:"id"`
				ListenAddr string `json:"listen_addr"`
				Network    string `json:"network"`
			} `json:"node_info"`
			RemoteIP string `json:"remote_ip"`
		} `json:"peers"`
	} `json:"result"`
}

// netInfoPeers returns id@host:port for the peers an RPC node is connected to,
// using the remote IP when the peer listens on an unspecified address.
func netInfoPeers(rpc, chainID string, timeout time.Duration) ([]string, error) {
	var ni netInfo
	if err := getJSON(strings.TrimRight(rpc, "/")+"/net_info", timeout, &ni); err != nil {
		return nil, err
	}
	var out []string
	for _, p := range ni.Result.Peers {
		if p.NodeInfo.ID == "" || (chainID != "" && p.NodeInfo.Network != chainID) {
			continue
		}
		host, port, ok := listenHostPort(p.NodeInfo.ListenAddr)
		if !ok {
			continue
		}
		if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
			host = p.RemoteIP
		}
		if host == "" {
			continue
		}
		out = append(out, p.NodeInfo.ID+"@"+net.JoinHostPort(host, port))
	}
	return out, nil
}

func listenHostPort(s string) (string, string, bool) {
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return "", "", false
	}
	if _, err := strconv.Atoi(port); err != nil {
		return "", "", false
	}
	return host, port, true
}

func getJSON(url string, timeout time.Duration, v any) error {
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "cometseed/"+buildVersion)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}
