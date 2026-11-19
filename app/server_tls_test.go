//go:build server

package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/logger"
)

// writePair writes c's certificate and key to the two files, then dates both at, so a change is
// seen whatever the file system's clock granularity, and whether or not the sizes changed.
func writePair(t *testing.T, certFile, keyFile string, c *issued, at time.Time) {
	t.Helper()
	keyDER, err := x509.MarshalECPrivateKey(c.key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	touch(t, at, certFile, keyFile)
}

func touch(t *testing.T, at time.Time, files ...string) {
	t.Helper()
	for _, f := range files {
		if err := os.Chtimes(f, at, at); err != nil {
			t.Fatal(err)
		}
	}
}

// servedBy is the serial of the certificate a TLS handshake with addr is served, trusting only the
// certificates given.
func servedBy(t *testing.T, addr string, trust ...*issued) string {
	t.Helper()
	roots := x509.NewCertPool()
	for _, c := range trust {
		roots.AddCert(c.cert)
	}
	conn, err := tls.Dial("tcp", addr, &tls.Config{RootCAs: roots, ServerName: "autodoc.test", MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatalf("the handshake: %v", err)
	}
	defer conn.Close()
	return conn.ConnectionState().PeerCertificates[0].SerialNumber.String()
}

// logLines is a logger's lines, kept.
type logLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *logLines) logger() logger.Logger {
	return logger.Adapt(func(sev logger.Severity, payload any) {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.lines = append(l.lines, fmt.Sprintf("%s: %v", sev, payload))
	})
}

func (l *logLines) count(s string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, x := range l.lines {
		if strings.Contains(x, s) {
			n++
		}
	}
	return n
}

// The certificate is read again when its files change, from the next handshake on, without a
// restart. Files that change and don't load keep the previous certificate served and are logged
// once as an error, however many handshakes follow, as does a key file gone; fixed, the new pair
// is served. Within the check's interval the files aren't looked at. The mutants, named here: no
// reload (the first certificate served for ever); a failure logged at every handshake.
func TestTheTLSCertificateIsReadAgainWhenItsFilesChange(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	a, b, c := issue(t, nil, false, []string{"autodoc.test"}, nil), issue(t, nil, false, []string{"autodoc.test"}, nil), issue(t, nil, false, []string{"autodoc.test"}, nil)
	base := time.Now().Add(-time.Hour)
	writePair(t, certFile, keyFile, a, base)
	var log logLines
	r, err := newCertReloader(certFile, keyFile, 0, log.logger())
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.NotFoundHandler(), TLSConfig: &tls.Config{GetCertificate: r.GetCertificate}}
	go func() { _ = srv.ServeTLS(ln, "", "") }()
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	addr := ln.Addr().String()
	if got := servedBy(t, addr, a, b, c); got != a.cert.SerialNumber.String() {
		t.Fatalf("served %s, want the first certificate", got)
	}

	writePair(t, certFile, keyFile, b, base.Add(time.Minute))
	if got := servedBy(t, addr, a, b, c); got != b.cert.SerialNumber.String() {
		t.Fatalf("after the files changed, served %s, want the new certificate", got)
	}
	if n := log.count("serving the new certificate (autodoc.test, until"); n != 1 {
		t.Errorf("the reload logged %d times, want once", n)
	}

	if err := os.WriteFile(certFile, []byte("-----BEGIN CERTIFICATE-----\nbroken\n-----END CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	touch(t, base.Add(2*time.Minute), certFile)
	for range 3 {
		if got := servedBy(t, addr, a, b, c); got != b.cert.SerialNumber.String() {
			t.Fatalf("with a broken file, served %s, want the previous certificate", got)
		}
	}
	if n := log.count("Error: "); n != 1 || log.count("don't load: the previous certificate is still served") != 1 {
		t.Errorf("a broken file logged %d errors over three handshakes, want one: %q", n, log.lines)
	}

	// a key file gone (a renewal between removing it and writing it) is the same, logged once
	if err := os.Remove(keyFile); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if got := servedBy(t, addr, a, b, c); got != b.cert.SerialNumber.String() {
			t.Fatalf("with the key file gone, served %s, want the previous certificate", got)
		}
	}
	if n := log.count("Error: "); n != 2 || log.count("http.tls_key: stat") != 1 {
		t.Errorf("a key file gone logged %d errors in all over three handshakes, want one more: %q", n, log.lines)
	}

	writePair(t, certFile, keyFile, c, base.Add(3*time.Minute))
	if got := servedBy(t, addr, a, b, c); got != c.cert.SerialNumber.String() {
		t.Fatalf("after the files were fixed, served %s, want the new certificate", got)
	}

	// within the interval, the files aren't looked at
	r.mu.Lock()
	r.every, r.checked = time.Hour, time.Now()
	r.mu.Unlock()
	writePair(t, certFile, keyFile, a, base.Add(4*time.Minute))
	if got := servedBy(t, addr, a, b, c); got != c.cert.SerialNumber.String() {
		t.Fatalf("within the interval, served %s, want the certificate before the change", got)
	}
}

// The server serves its certificate through the reloader: a renewal written while it runs is
// served by its search API without a restart. The mutant, named here: the API served from the
// files as they were at its start (ServeTLS with the file names).
func TestTheServerServesARenewedCertificateWithoutARestart(t *testing.T) {
	was := tlsCheckEvery
	tlsCheckEvery = 0
	t.Cleanup(func() { tlsCheckEvery = was })
	f := newServerFixture(t, serverView)
	t.Setenv("AUTODOC_TEST_HTTP_TOKEN", httpToken)
	dir := filepath.Dir(f.path)
	a, b := issue(t, nil, false, []string{"autodoc.test"}, nil), issue(t, nil, false, []string{"autodoc.test"}, nil)
	base := time.Now().Add(-time.Hour)
	writePair(t, filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key"), a, base)
	f.extra = strings.Replace(httpBlock, `listen = "127.0.0.1:0"`, "listen = \"127.0.0.1:0\"\ntls_cert = \"tls.crt\"\ntls_key = \"tls.key\"", 1)
	f.configure("fake-model", f.url)
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	done, stderr := f.serve(ctx)
	f.started(stderr)
	m := listening.FindStringSubmatch(stderr.String())
	if m == nil {
		t.Fatalf("the server does not say where it listens: %s", stderr)
	}
	if got := servedBy(t, m[1], a, b); got != a.cert.SerialNumber.String() {
		t.Fatalf("served %s, want the first certificate", got)
	}
	writePair(t, filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key"), b, base.Add(time.Minute))
	if got := servedBy(t, m[1], a, b); got != b.cert.SerialNumber.String() {
		t.Fatalf("after a renewal, served %s, want the renewed certificate", got)
	}
	cancel()
	if code := exitOf(t, done, stderr); code != 0 {
		t.Fatalf("--server exited %d: %s", code, stderr)
	}
}
