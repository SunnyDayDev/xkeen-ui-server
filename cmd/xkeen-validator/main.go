// xkeen-validator runs the isolated Docker worker on its operator's host.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/validation"
)

func main() {
	if run() != nil {
		fmt.Fprintln(os.Stderr, "validator service unavailable")
		os.Exit(1)
	}
}
func run() error {
	var docker, engine, root, image, socket, address, cert, key, ca, pin string
	flag.StringVar(&docker, "docker", "/usr/bin/docker", "absolute Docker CLI path")
	flag.StringVar(&engine, "engine", "/var/run/docker.sock", "local Docker Engine socket")
	flag.StringVar(&root, "state", "", "dedicated directory owned by worker, mode 0700")
	flag.StringVar(&image, "image", "", "trusted immutable validator image ID (sha256:...)")
	flag.StringVar(&socket, "socket", "", "private local Unix socket")
	flag.StringVar(&address, "listen", "", "mTLS listen address")
	flag.StringVar(&cert, "cert", "", "worker PEM certificate")
	flag.StringVar(&key, "key", "", "worker PEM private key")
	flag.StringVar(&ca, "client-ca", "", "trusted client CA PEM")
	flag.StringVar(&pin, "client-sha256", "", "authorized client leaf certificate SHA-256 in hex")
	flag.Parse()
	if flag.NArg() != 0 || (socket == "") == (address == "") {
		return errors.New("select one listener")
	}
	w, err := validation.NewDockerWorker(docker, engine, image, root)
	if err != nil {
		return err
	}
	defer w.Close()
	srv := &http.Server{Handler: w.Handler(), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 8192, ErrorLog: log.New(io.Discard, "", 0)}
	var ln net.Listener
	if socket != "" {
		info, err := os.Lstat(filepath.Dir(socket))
		if err != nil || !filepath.IsAbs(socket) || !info.IsDir() || info.Mode().Perm()&0022 != 0 {
			return errors.New("socket directory")
		}
		// The parent directory must not be writable by network clients or other users.
		if st, err := os.Lstat(socket); err == nil {
			if st.Mode()&os.ModeSocket == 0 {
				return errors.New("occupied socket")
			}
			if live, e := net.DialTimeout("unix", socket, time.Second); e == nil {
				live.Close()
				return errors.New("live socket")
			}
			if e := os.Remove(socket); e != nil {
				return e
			}
		}
		ln, err = net.Listen("unix", socket)
		if err != nil {
			return err
		}
		if err = os.Chmod(socket, 0660); err != nil {
			ln.Close()
			return err
		}
	} else {
		pair, err := tls.LoadX509KeyPair(cert, key)
		if err != nil {
			return err
		}
		pem, err := os.ReadFile(ca)
		if err != nil {
			return err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return errors.New("CA")
		}
		cfg, err := validation.WorkerTLS(pair, pool, pin)
		if err != nil {
			return err
		}
		ln, err = tls.Listen("tcp", address, cfg)
		if err != nil {
			return err
		}
	}
	defer ln.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv.BaseContext = func(net.Listener) context.Context { return ctx }
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}
