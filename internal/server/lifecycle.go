package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

// Serve owns listener and returns after serving stops and its resources close.
func Serve(ctx context.Context, listener net.Listener, handler http.Handler, shutdownTimeout time.Duration) error {
	defer listener.Close()
	if ctx.Err() != nil {
		return nil
	}
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	defer srv.Close()
	served := make(chan error, 1)
	go func() { served <- srv.Serve(listener) }()
	select {
	case err := <-served:
		return err
	case <-ctx.Done():
		// The run context is canceled; draining needs its own deadline.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		shutdownErr := srv.Shutdown(shutdownCtx)
		if shutdownErr != nil {
			_ = srv.Close()
		}
		serveErr := <-served
		if !errors.Is(serveErr, http.ErrServerClosed) {
			return errors.Join(serveErr, shutdownErr)
		}
		return shutdownErr
	}
}
