package app

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/enterprise/config"
)

// issued is a certificate and its key: issued by parent (nil: self-signed), naming dns and ips.
type issued struct {
	cert *x509.Certificate
	der  []byte
	key  *ecdsa.PrivateKey
}

func issue(t *testing.T, parent *issued, ca bool, dns []string, ips []net.IP) *issued {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tmpl := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "autodoc test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: dns, IPAddresses: ips, IsCA: ca, BasicConstraintsValid: true}
	if ca {
		tmpl.KeyUsage |= x509.KeyUsageCertSign
	}
	signer, signerKey := tmpl, key
	if parent != nil {
		signer, signerKey = parent.cert, parent.key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, signer, &key.PublicKey, signerKey)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return &issued{cert: c, der: der, key: key}
}

// pemOf is the certificates as a PEM file, written in dir, and its path.
func pemOf(t *testing.T, dir, name string, certs ...*issued) string {
	t.Helper()
	var b bytes.Buffer
	for _, c := range certs {
		_ = pem.Encode(&b, &pem.Block{Type: "CERTIFICATE", Bytes: c.der})
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// healthServer serves /v1/health answering status, on bind, over TLS with serve (leaf first, then
// its chain) when given; it returns its port.
func healthServer(t *testing.T, bind, status string, serve ...*issued) string {
	t.Helper()
	ln, err := net.Listen("tcp", bind)
	if err != nil {
		t.Fatal(err)
	}
	code := http.StatusOK
	if status == "unavailable" {
		code = http.StatusServiceUnavailable
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(code)
		_, _ = w.Write([]byte(`{"status":"` + status + `","checks":{}}`))
	})}
	if len(serve) > 0 {
		chain := tls.Certificate{PrivateKey: serve[0].key}
		for _, c := range serve {
			chain.Certificate = append(chain.Certificate, c.der)
		}
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{chain}})
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
}

// probeOf runs the probe against h, and is its exit code and what it said.
func probeOf(h *config.HTTP) (int, string) {
	var out, errOut bytes.Buffer
	code := probe(context.Background(), h, &out, &errOut)
	return code, out.String() + errOut.String()
}

// --server --health asks the listener for its health: 0 for ok and degraded, 1 for unavailable and
// for a server that does not answer. The mutants, named here: degraded answered 1 (a degraded server
// still answers searches); unavailable answered 0.
func TestTheHealthProbeAnswersByStatus(t *testing.T) {
	for status, want := range map[string]int{"ok": 0, "degraded": 0, "unavailable": 1} {
		port := healthServer(t, "127.0.0.1:0", status)
		if code, said := probeOf(&config.HTTP{Listen: "127.0.0.1:" + port}); code != want || !strings.Contains(said, status) {
			t.Errorf("%s: exit %d, said %s", status, code, said)
		}
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := ln.Addr().String()
	ln.Close()
	if code, _ := probeOf(&config.HTTP{Listen: closed}); code != 1 {
		t.Errorf("no server: exit %d", code)
	}
}

// Over TLS, the probe trusts the served leaf alone and checks its own name: a server bound to every
// address, serving a self-signed leaf for search.example, is probed through loopback with that name;
// a leaf naming an IP only is checked by its IP; a leaf issued by a CA, its file holding the CA
// after it, is trusted, but a sibling leaf of the same CA served instead is refused (the CA is no
// anchor); a leaf naming nothing is refused, naming tls_cert. The mutants, named here: every
// certificate in the file an anchor (the sibling trusted); verification skipped.
func TestTheHealthProbeTrustsTheServedLeafAlone(t *testing.T) {
	dir := t.TempDir()
	self := issue(t, nil, false, []string{"search.example"}, nil)
	port := healthServer(t, "0.0.0.0:0", "ok", self)
	if code, said := probeOf(&config.HTTP{Listen: "0.0.0.0:" + port, TLSCert: pemOf(t, dir, "self.pem", self)}); code != 0 {
		t.Errorf("a self-signed leaf on every address: exit %d: %s", code, said)
	}

	ipOnly := issue(t, nil, false, nil, []net.IP{net.ParseIP("127.0.0.1")})
	port = healthServer(t, "127.0.0.1:0", "ok", ipOnly)
	if code, said := probeOf(&config.HTTP{Listen: "127.0.0.1:" + port, TLSCert: pemOf(t, dir, "ip.pem", ipOnly)}); code != 0 {
		t.Errorf("a leaf naming an IP only: exit %d: %s", code, said)
	}

	ca := issue(t, nil, true, nil, nil)
	leaf := issue(t, ca, false, []string{"search.example"}, nil)
	sibling := issue(t, ca, false, []string{"search.example"}, nil)
	chain := pemOf(t, dir, "chain.pem", leaf, ca)
	port = healthServer(t, "0.0.0.0:0", "ok", leaf, ca)
	if code, said := probeOf(&config.HTTP{Listen: "0.0.0.0:" + port, TLSCert: chain}); code != 0 {
		t.Errorf("the configured leaf, its CA after it: exit %d: %s", code, said)
	}
	port = healthServer(t, "0.0.0.0:0", "ok", sibling, ca)
	if code, said := probeOf(&config.HTTP{Listen: "0.0.0.0:" + port, TLSCert: chain}); code != 1 || !strings.Contains(said, "certificate") {
		t.Errorf("a sibling leaf of the same CA served: exit %d: %s", code, said)
	}

	nameless := issue(t, nil, false, nil, nil)
	if code, said := probeOf(&config.HTTP{Listen: "127.0.0.1:1", TLSCert: pemOf(t, dir, "nameless.pem", nameless)}); code != 1 || !strings.Contains(said, "http.tls_cert") {
		t.Errorf("a leaf naming nothing: exit %d: %s", code, said)
	}
}

// --server --health on a configuration with no [http] is 1, saying so; --check and --health
// together are refused, as two answers to one question.
func TestTheHealthProbeNeedsHTTP(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AUTODOC_TEST_PROBE_DSN", "postgres://u@h/db")
	cfg := filepath.Join(dir, "server.toml")
	if err := os.WriteFile(cfg, []byte("[postgres]\ndsn_env = \"AUTODOC_TEST_PROBE_DSN\"\n[embedding]\nprovider = \"ollama\"\nmodel = \"m\"\n[[source]]\ntenant = \"t\"\nview = \"t.view\"\naccess = [\"label_id\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := run(context.Background(), []string{"--server", "--health", cfg}, testOptions, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "has no [http]") {
		t.Errorf("no [http]: exit %d: %s", code, errOut.String())
	}
	errOut.Reset()
	if code := run(context.Background(), []string{"--server", "--check", "--health", cfg}, testOptions, &out, &errOut); code != 2 {
		t.Errorf("--check with --health: exit %d: %s", code, errOut.String())
	}
}
