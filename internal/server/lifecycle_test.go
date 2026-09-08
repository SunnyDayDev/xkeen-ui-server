package server_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/server"
)

func TestServeIdleShutdown(t *testing.T) {
	running := startServer(t, server.NewHandler(), time.Second)
	waitForHealth(t, running.listener.Addr().String())
	running.cancel()
	if err := running.wait(t); err != nil {
		t.Errorf("shutdown: %v", err)
	}
	assertPortFree(t, running.listener.Addr().String())
}

func TestServeAlreadyCanceled(t *testing.T) {
	listener := listen(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := server.Serve(ctx, listener, server.NewHandler(), time.Second); err != nil {
		t.Errorf("already canceled: %v", err)
	}
	assertPortFree(t, listener.Addr().String())
}

func TestServeAcceptError(t *testing.T) {
	want := errors.New("accept failed")
	listener := &failingListener{Listener: listen(t), err: want}
	if err := server.Serve(context.Background(), listener, server.NewHandler(), time.Second); !errors.Is(err, want) {
		t.Errorf("Serve error = %v, want %v", err, want)
	}
	if !listener.closed.Load() {
		t.Error("listener was not closed")
	}
	assertPortFree(t, listener.Addr().String())
}

func TestServeDrainsRequests(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		if release != nil {
			close(release)
		}
	}()
	gate := release
	running := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-gate
		_, _ = io.WriteString(w, "finished")
	}), time.Second)
	response := beginRequest(t, running.listener.Addr().String())
	awaitEntered(t, entered)
	running.cancel()
	waitListenerClosed(t, running.listener.Addr().String())
	select {
	case <-running.done:
		t.Error("Serve returned before the active request finished")
	default:
	}
	close(release)
	release = nil
	got := awaitResponse(t, response)
	if got.err != nil || got.body != "finished" {
		t.Errorf("active response = %q, %v; want finished", got.body, got.err)
	}
	if err := running.wait(t); err != nil {
		t.Errorf("drain: %v", err)
	}
	assertPortFree(t, running.listener.Addr().String())
}

func TestServeShutdownDeadline(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	const timeout = 50 * time.Millisecond
	running := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
	}), timeout)
	response := beginRequest(t, running.listener.Addr().String())
	awaitEntered(t, entered)
	started := time.Now()
	running.cancel()
	if err := running.wait(t); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("shutdown error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(started); elapsed < timeout {
		t.Errorf("shutdown returned in %v before deadline %v", elapsed, timeout)
	}
	if got := awaitResponse(t, response); got.err == nil {
		t.Errorf("unfinished request succeeded: %q", got.body)
	}
	assertPortFree(t, running.listener.Addr().String())
}

type requestResult struct {
	body string
	err  error
}

func beginRequest(t *testing.T, address string) <-chan requestResult {
	t.Helper()
	client := httpClient()
	client.Timeout = 2 * time.Second
	t.Cleanup(client.CloseIdleConnections)
	result := make(chan requestResult, 1)
	go func() {
		response, err := client.Get("http://" + address + "/healthz")
		if err != nil {
			result <- requestResult{err: err}
			return
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		result <- requestResult{body: string(body), err: err}
	}()
	return result
}

func awaitEntered(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
}

func awaitResponse(t *testing.T, response <-chan requestResult) requestResult {
	t.Helper()
	select {
	case result := <-response:
		return result
	case <-time.After(3 * time.Second):
		t.Fatal("request did not finish")
		return requestResult{}
	}
}

func waitListenerClosed(t *testing.T, address string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 50*time.Millisecond)
		if err != nil {
			return
		}
		_ = conn.Close()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("listener still accepts connections")
}

type failingListener struct {
	net.Listener
	err    error
	closed atomic.Bool
}

func (l *failingListener) Accept() (net.Conn, error) { return nil, l.err }
func (l *failingListener) Close() error {
	l.closed.Store(true)
	return l.Listener.Close()
}

type runningServer struct {
	listener net.Listener
	cancel   context.CancelFunc
	done     chan struct{}
	err      error
}

func startServer(t *testing.T, handler http.Handler, timeout time.Duration) *runningServer {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &runningServer{listener: listen(t), cancel: cancel, done: make(chan struct{})}
	go func() {
		r.err = server.Serve(ctx, r.listener, handler, timeout)
		close(r.done)
	}()
	t.Cleanup(func() {
		cancel()
		_ = r.listener.Close()
		select {
		case <-r.done:
		case <-time.After(2 * time.Second):
			t.Error("Serve did not return during cleanup")
		}
	})
	return r
}

func (r *runningServer) wait(t *testing.T) error {
	t.Helper()
	select {
	case <-r.done:
		return r.err
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return")
		return nil
	}
}

func listen(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func assertPortFree(t *testing.T, address string) {
	t.Helper()
	l, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("listener was not released: %v", err)
	}
	_ = l.Close()
}

func httpClient() *http.Client {
	return &http.Client{
		Timeout:   200 * time.Millisecond,
		Transport: &http.Transport{DisableKeepAlives: true},
	}
}

func waitForHealth(t *testing.T, address string) {
	t.Helper()
	client := httpClient()
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get("http://" + address + "/healthz")
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("server did not become available")
}
