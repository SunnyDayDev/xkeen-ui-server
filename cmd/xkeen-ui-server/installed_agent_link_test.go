package main

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

// installedAgentLink gates only the synthetic agent channel. It does not
// terminate TLS or provide a way to exercise a listener outside the fixture.
type installedAgentLink struct {
	listener  net.Listener
	target    string
	mu        sync.Mutex
	available bool
	epoch     uint64
	live      map[net.Conn]net.Conn
	done      sync.WaitGroup
}

func newInstalledAgentLink(t *testing.T, target string) *installedAgentLink {
	t.Helper()
	endpoint, err := netip.ParseAddrPort(target)
	if err != nil || !endpoint.Addr().IsLoopback() || endpoint.Port() == 0 {
		t.Fatal("installed agent link needs a fixed loopback test peer")
	}

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gate := &installedAgentLink{listener: listener, target: target, available: true, live: make(map[net.Conn]net.Conn)}
	gate.done.Go(gate.serve)
	t.Cleanup(func() {
		_ = listener.Close()
		gate.change(false)
		gate.done.Wait()
	})
	return gate
}

func (g *installedAgentLink) origin() string { return "https://" + g.listener.Addr().String() }

func (g *installedAgentLink) change(available bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.epoch++
	g.available = available
	if !available {
		for incoming, outgoing := range g.live {
			incoming.Close()
			outgoing.Close()
		}
	}
}

func (g *installedAgentLink) serve() {
	for {
		incoming, err := g.listener.Accept()
		if err != nil {
			return
		}
		g.done.Go(func() { g.bridge(incoming) })
	}
}

func (g *installedAgentLink) bridge(incoming net.Conn) {
	defer incoming.Close()
	g.mu.Lock()
	epoch, available := g.epoch, g.available
	g.mu.Unlock()
	if !available {
		return
	}
	outgoing, err := net.DialTimeout("tcp", g.target, 500*time.Millisecond)
	if err != nil {
		return
	}
	defer outgoing.Close()
	g.mu.Lock()
	if !g.available || g.epoch != epoch {
		g.mu.Unlock()
		return
	}
	g.live[incoming] = outgoing
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		delete(g.live, incoming)
		g.mu.Unlock()
	}()
	sent := make(chan struct{})
	go func() {
		_, _ = io.Copy(outgoing, incoming)
		outgoing.Close()
		close(sent)
	}()
	_, _ = io.Copy(incoming, outgoing)
	incoming.Close()
	<-sent
}

func TestInstalledAgentLinkDropsOnlyItsLiveHTTPSConnections(t *testing.T) {
	cert, key, roots := fixtureServerCertificate(t)
	seen := make(chan struct{})
	central := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/watch" {
			close(seen)
			<-r.Context().Done()
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	pair, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	central.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	central.StartTLS()
	t.Cleanup(central.Close)
	link := newInstalledAgentLink(t, strings.TrimPrefix(central.URL, "https://"))
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	defer transport.CloseIdleConnections()
	request := func(url string) error {
		res, err := client.Get(url)
		if res != nil {
			res.Body.Close()
		}
		return err
	}
	if err := request(link.origin() + "/healthz"); err != nil {
		t.Fatal(err)
	}
	ended := make(chan error, 1)
	go func() { ended <- request(link.origin() + "/watch") }()
	select {
	case <-seen:
	case <-time.After(time.Second):
		t.Fatal("long request did not reach independent central endpoint")
	}
	link.change(false)
	select {
	case err := <-ended:
		if err == nil {
			t.Fatal("outage left existing TLS connection running")
		}
	case <-time.After(time.Second):
		t.Fatal("outage did not cut existing request")
	}
	if err := request(link.origin() + "/healthz"); err == nil {
		t.Fatal("outage admitted new TLS connection")
	}
	if err := request(central.URL + "/healthz"); err != nil {
		t.Fatal("agent-only outage cut UI access")
	}
	link.change(true)
	if err := request(link.origin() + "/healthz"); err != nil {
		t.Fatal("agent origin did not recover", err)
	}
}
