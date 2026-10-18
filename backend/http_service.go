package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
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

// httpsRedirect only redirects. It never exposes the app or API over HTTP.
// The destination uses the requested hostname and the actual TLS port, without
// accepting forwarded-host headers or retaining user information in a URL.
func httpsRedirect(port string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		authority, err := url.Parse("http://" + r.Host)
		if err != nil || authority.User != nil || authority.Hostname() == "" || authority.Path != "" || authority.RawQuery != "" || authority.ForceQuery || authority.Fragment != "" {
			http.Error(w, "Invalid request host", http.StatusBadRequest)
			return
		}
		target := *r.URL
		target.Scheme = "https"
		target.Host = net.JoinHostPort(authority.Hostname(), port)
		target.User = nil
		target.Opaque = ""
		if port == "443" {
			target.Host = strings.TrimSuffix(target.Host, ":443")
		}
		http.Redirect(w, r, target.String(), http.StatusTemporaryRedirect)
	})
}

type webService struct {
	app, redirect                 *http.Server
	appListener, redirectListener net.Listener
}

// openWebService validates certificates and binds every required port before
// the supervisor can mark the release ready. HTTP-only mode keeps one listener.
func openWebService(address, httpsAddress, redirectAddress, certFile, keyFile string, handler http.Handler) (*webService, error) {
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
	} else if httpsAddress != "" || redirectAddress != "" {
		return nil, errors.New("--https-listen and --http-redirect-listen require --cert and --key")
	}
	listener, err := net.Listen("tcp", service.app.Addr)
	if err != nil {
		return nil, err
	}
	service.appListener = listener
	if certFile != "" {
		_, port, _ := net.SplitHostPort(listener.Addr().String())
		if redirectAddress == "" {
			host, _, _ := net.SplitHostPort(service.app.Addr)
			redirectAddress = net.JoinHostPort(host, "80")
		}
		service.redirect = &http.Server{Addr: redirectAddress, Handler: httpsRedirect(port), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 8 << 10}
		service.redirectListener, err = net.Listen("tcp", redirectAddress)
		if err != nil {
			service.Close()
			return nil, fmt.Errorf("HTTP redirect listener: %w", err)
		}
	}
	return service, nil
}

func (s *webService) Close() {
	_ = s.appListener.Close()
	if s.redirectListener != nil {
		_ = s.redirectListener.Close()
	}
}

// Serve owns both listeners. A failure of either listener stops the service;
// cancellation drains both servers, then forcibly closes any remaining clients.
func (s *webService) Serve(ctx context.Context) error {
	results := make(chan error, 2)
	count := 1
	go func() {
		if s.app.TLSConfig != nil {
			results <- s.app.ServeTLS(s.appListener, "", "")
		} else {
			results <- s.app.Serve(s.appListener)
		}
	}()
	if s.redirect != nil {
		count++
		go func() { results <- s.redirect.Serve(s.redirectListener) }()
	}
	var result error
	select {
	case result = <-results:
		count--
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if s.redirect != nil {
		_ = s.redirect.Shutdown(shutdown)
		_ = s.redirect.Close()
	}
	_ = s.app.Shutdown(shutdown)
	_ = s.app.Close()
	for range count {
		<-results
	}
	if errors.Is(result, http.ErrServerClosed) {
		return nil
	}
	return result
}
