package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/yongjohnlee80/autodoc/enterprise/config"
	"github.com/yongjohnlee80/autodoc/enterprise/ops"
)

// healthTimeout is how long --server --health waits for the server's answer.
//
// NOTE: a deadline, not a wait. A container's healthcheck runs the probe on a schedule; a server
// that does not answer must fail the probe in time, not hang it until the runtime kills it.
const healthTimeout = 5 * time.Second

// probeHealth is --server --health: it asks the server its configuration at path describes for its
// health (GET /v1/health), prints the answer, and is 0 when the server is ok or degraded, 1 otherwise
// (no [http], no answer, unavailable). It runs where the server does, in its container or beside
// its unit: the target is the listener's own address, or loopback when it listens on every one.
func probeHealth(ctx context.Context, path string, out, errOut io.Writer) int {
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintln(errOut, "autodoc --server --health:", err)
		return 1
	}
	if cfg.HTTP == nil {
		fmt.Fprintf(errOut, "autodoc --server --health: %s has no [http]: the server answers no health request\n", path)
		return 1
	}
	return probe(ctx, cfg.HTTP, out, errOut)
}

// probe asks the server h describes for its health, as probeHealth.
func probe(ctx context.Context, h *config.HTTP, out, errOut io.Writer) int {
	url, client, err := healthTarget(h)
	if err != nil {
		fmt.Fprintln(errOut, "autodoc --server --health:", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintln(errOut, "autodoc --server --health:", err)
		return 1
	}
	res, err := client.Do(req)
	if err != nil {
		fmt.Fprintln(errOut, "autodoc --server --health:", err)
		return 1
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<16))
	fmt.Fprintln(out, string(body))
	var health ops.Health
	if json.Unmarshal(body, &health) != nil {
		fmt.Fprintf(errOut, "autodoc --server --health: %s answered %s, not a health report\n", url, res.Status)
		return 1
	}
	if health.Status == ops.StatusOK || health.Status == ops.StatusDegraded {
		return 0
	}
	return 1
}

// healthTarget is where the probe asks, and the client it asks with.
//
// The host is the listener's own when it names one, loopback (of the same family) when it listens
// on every address: the probe runs on the server's host. The scheme is https when the server has a
// certificate. Its TLS check never skips verification: the only trust anchor is the served leaf,
// the first certificate in tls_cert (an intermediate after it is not one, so a sibling certificate
// from the same CA is refused), and the name checked is the leaf's first DNS name, else its first IP
// address. It needs no system CA bundle, which a distroless image does not have to rely on.
func healthTarget(h *config.HTTP) (string, *http.Client, error) {
	host, port, err := net.SplitHostPort(h.Listen)
	if err != nil {
		return "", nil, fmt.Errorf("http.listen %q: %w", h.Listen, err)
	}
	ip := net.ParseIP(host)
	switch {
	case host == "" || (ip != nil && ip.IsUnspecified() && ip.To4() != nil):
		host = "127.0.0.1"
	case ip != nil && ip.IsUnspecified():
		host = "::1"
	}
	tr := &http.Transport{Proxy: nil}
	scheme := "http"
	if h.TLSCert != "" {
		leaf, err := servedLeaf(h.TLSCert)
		if err != nil {
			return "", nil, err
		}
		roots := x509.NewCertPool()
		roots.AddCert(leaf)
		var name string
		switch {
		case len(leaf.DNSNames) > 0:
			name = leaf.DNSNames[0]
		case len(leaf.IPAddresses) > 0:
			name = leaf.IPAddresses[0].String()
		default:
			return "", nil, fmt.Errorf("http.tls_cert %s: its certificate names no DNS name or IP address to check", h.TLSCert)
		}
		tr.TLSClientConfig = &tls.Config{RootCAs: roots, ServerName: name, MinVersion: tls.VersionTLS12}
		scheme = "https"
	}
	return scheme + "://" + net.JoinHostPort(host, port) + "/v1/health", &http.Client{Transport: tr}, nil
}

// servedLeaf is the first certificate in the PEM file at path: the one the server serves.
func servedLeaf(path string) (*x509.Certificate, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("http.tls_cert: %w", err)
	}
	for {
		var block *pem.Block
		block, b = pem.Decode(b)
		if block == nil {
			return nil, errors.New("http.tls_cert " + path + ": no certificate in it")
		}
		if block.Type == "CERTIFICATE" {
			c, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("http.tls_cert %s: %w", path, err)
			}
			return c, nil
		}
	}
}
