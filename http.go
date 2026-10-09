package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type collector struct {
	seeds    []*Seed
	addrbook *prometheus.Desc
	peers    *prometheus.Desc
	lastBoot *prometheus.Desc
	info     *prometheus.Desc
}

func newCollector(seeds []*Seed) *collector {
	labels := []string{"network", "chain_id"}
	return &collector{
		seeds:    seeds,
		addrbook: prometheus.NewDesc("cometseed_addrbook_size", "Addresses in the address book.", labels, nil),
		peers:    prometheus.NewDesc("cometseed_peers", "Connected peers by direction.", append(labels, "direction"), nil),
		lastBoot: prometheus.NewDesc("cometseed_last_bootstrap_timestamp_seconds", "Unix time of the last bootstrap.", labels, nil),
		info:     prometheus.NewDesc("cometseed_info", "Seed identity.", append(labels, "node_id", "version"), nil),
	}
}

func (c *collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.addrbook
	ch <- c.peers
	ch <- c.lastBoot
	ch <- c.info
}

func (c *collector) Collect(ch chan<- prometheus.Metric) {
	for _, s := range c.seeds {
		st := s.Status()
		ch <- prometheus.MustNewConstMetric(c.addrbook, prometheus.GaugeValue, float64(st.AddrBook), st.Name, st.ChainID)
		ch <- prometheus.MustNewConstMetric(c.peers, prometheus.GaugeValue, float64(st.Outbound), st.Name, st.ChainID, "outbound")
		ch <- prometheus.MustNewConstMetric(c.peers, prometheus.GaugeValue, float64(st.Inbound), st.Name, st.ChainID, "inbound")
		ch <- prometheus.MustNewConstMetric(c.peers, prometheus.GaugeValue, float64(st.Dialing), st.Name, st.ChainID, "dialing")
		if !st.LastBootstrap.IsZero() {
			ch <- prometheus.MustNewConstMetric(c.lastBoot, prometheus.GaugeValue, float64(st.LastBootstrap.Unix()), st.Name, st.ChainID)
		}
		ch <- prometheus.MustNewConstMetric(c.info, prometheus.GaugeValue, 1, st.Name, st.ChainID, st.NodeID, buildVersion)
	}
}

func serveHTTP(listen string, seeds []*Seed) *http.Server {
	reg := prometheus.NewRegistry()
	reg.MustRegister(newCollector(seeds))

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		out := struct {
			Version  string       `json:"version"`
			Networks []SeedStatus `json:"networks"`
		}{Version: buildVersion}
		for _, s := range seeds {
			out.Networks = append(out.Networks, s.Status())
		}
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		for _, s := range seeds {
			if s.Status().AddrBook == 0 {
				http.Error(w, "empty address book: "+s.net.Name, http.StatusServiceUnavailable)
				return
			}
		}
		_, _ = w.Write([]byte("ok\n"))
	})

	srv := &http.Server{Addr: listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	return srv
}
