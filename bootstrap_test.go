package main

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

const netInfoBody = `{"result":{"peers":[
 {"node_info":{"id":"aa","listen_addr":"tcp://0.0.0.0:26656","network":"x-1"},"remote_ip":"1.2.3.4"},
 {"node_info":{"id":"bb","listen_addr":"5.6.7.8:12956","network":"x-1"},"remote_ip":"9.9.9.9"},
 {"node_info":{"id":"cc","listen_addr":"tcp://1.1.1.1:26656","network":"other-1"},"remote_ip":"1.1.1.1"},
 {"node_info":{"id":"dd","listen_addr":"garbage","network":"x-1"},"remote_ip":"2.2.2.2"}
]}}`

const registryBody = `{"chain_id":"x-1",
 "peers":{"seeds":[{"id":"s1","address":"seed.x:26656"}],"persistent_peers":[{"id":"p1","address":"peer.x:26656"}]},
 "apis":{"rpc":[{"address":"https://rpc.x"}]}}`

func TestNetInfoPeers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/net_info" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(netInfoBody))
	}))
	defer srv.Close()

	got, err := netInfoPeers(srv.URL+"/", "x-1", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"aa@1.2.3.4:26656", "bb@5.6.7.8:12956"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestNetInfoHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()
	if _, err := netInfoPeers(srv.URL, "x-1", 5*time.Second); err == nil {
		t.Fatal("want an error on HTTP 403")
	}
}

func TestFetchRegistryChain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/testnets/x/chain.json" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(registryBody))
	}))
	defer srv.Close()

	rc, err := fetchRegistryChain(srv.URL+"/", "/testnets/x/", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if rc.ChainID != "x-1" || rc.Peers.Seeds[0].String() != "s1@seed.x:26656" ||
		rc.Peers.PersistentPeers[0].String() != "p1@peer.x:26656" || rc.APIs.RPC[0].Address != "https://rpc.x" {
		t.Fatalf("unexpected entry: %+v", rc)
	}
	if _, err := fetchRegistryChain(srv.URL, "missing", 5*time.Second); err == nil {
		t.Fatal("want an error for a missing chain")
	}
}

func TestListenHostPort(t *testing.T) {
	for in, want := range map[string][2]string{
		"tcp://0.0.0.0:26656": {"0.0.0.0", "26656"},
		"1.2.3.4:1":           {"1.2.3.4", "1"},
		"[::1]:26656":         {"::1", "26656"},
	} {
		h, p, ok := listenHostPort(in)
		if !ok || h != want[0] || p != want[1] {
			t.Errorf("%s: got %s %s %v", in, h, p, ok)
		}
	}
	if _, _, ok := listenHostPort("nope"); ok {
		t.Error("want failure for a bad address")
	}
}
