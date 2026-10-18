package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

const defaultHTTPSPort = "443"

// httpsListenAddress uses the standard secure port on the configured bind host.
func httpsListenAddress(address, configured string) (string, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return "", fmt.Errorf("HTTP listen address: %w", err)
	}
	if configured == "" {
		return net.JoinHostPort(host, defaultHTTPSPort), nil
	}
	if _, _, err := net.SplitHostPort(configured); err != nil {
		return "", fmt.Errorf("HTTPS listen address: %w", err)
	}
	return configured, nil
}

type webService struct {
	app         *http.Server
	appListener net.Listener
}

// openWebService binds one listener before the supervisor can report readiness.
// TLS mode exposes only HTTPS; HTTP remains available for local development.
func openWebService(address, httpsAddress, certFile, keyFile string, handler http.Handler) (*webService, error) {
	if (certFile == "") != (keyFile == "") {
		return nil, errors.New("HTTPS requires both --cert and --key")
	}
	service := &webService{app: &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 45 * time.Second, MaxHeaderBytes: 32 << 10}}
	if certFile != "" {
		pair, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("HTTPS certificate: %w", err)
		}
		service.app.Addr, err = httpsListenAddress(address, httpsAddress)
		if err != nil {
			return nil, err
		}
		service.app.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}
	} else if httpsAddress != "" {
		return nil, errors.New("--https-listen requires --cert and --key")
	}
	listener, err := net.Listen("tcp", service.app.Addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", service.app.Addr, err)
	}
	service.appListener = listener
	return service, nil
}

func (s *webService) Close() { _ = s.appListener.Close() }

func (s *webService) Serve(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		<-ctx.Done()
		shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = s.app.Shutdown(shutdown)
		_ = s.app.Close()
	}()
	var err error
	if s.app.TLSConfig != nil {
		err = s.app.ServeTLS(s.appListener, "", "")
	} else {
		err = s.app.Serve(s.appListener)
	}
	cancel()
	<-stopped
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
