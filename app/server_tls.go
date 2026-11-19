//go:build server

package app

import (
	"crypto/tls"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/yongjohnlee80/golib/logger"
)

// tlsCheckEvery is how often, at most, a handshake looks at the certificate's files for a change
// (a variable for the cells, which shorten it).
var tlsCheckEvery = 10 * time.Second

// certReloader serves [http] tls_cert and tls_key, read again when either file changes: a renewed
// certificate (a public CA's, every few months) is served from the first handshake after the files
// change, without a restart. A handshake looks at the files at most once in every; the check is
// their modification times and sizes, through symlinks, as a renewal tool that repoints a link
// leaves them. Files that change and don't load (a key not yet written beside its new certificate,
// a broken file) keep the previous certificate served, and are logged once, as an error, until they
// load or change again.
type certReloader struct {
	certFile, keyFile string
	every             time.Duration
	log               logger.Logger

	mu      sync.Mutex
	cert    *tls.Certificate
	seen    filesSeen // the files as last read, loaded or not
	checked time.Time // when a handshake last looked at the files
	failed  string    // the error last logged, until the files load
}

// filesSeen is what a change of the certificate's files is told by.
type filesSeen struct {
	certMod, keyMod   time.Time
	certSize, keySize int64
}

// newCertReloader reads the pair once: a pair that doesn't load fails the server's start, as it
// did before it was reloaded.
func newCertReloader(certFile, keyFile string, every time.Duration, log logger.Logger) (*certReloader, error) {
	r := &certReloader{certFile: certFile, keyFile: keyFile, every: every, log: log}
	seen, err := r.stat()
	if err != nil {
		return nil, err
	}
	c, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("http.tls_cert, http.tls_key: %w", err)
	}
	r.cert, r.seen, r.checked = &c, seen, time.Now()
	return r, nil
}

func (r *certReloader) stat() (filesSeen, error) {
	c, err := os.Stat(r.certFile)
	if err != nil {
		return filesSeen{}, fmt.Errorf("http.tls_cert: %w", err)
	}
	k, err := os.Stat(r.keyFile)
	if err != nil {
		return filesSeen{}, fmt.Errorf("http.tls_key: %w", err)
	}
	return filesSeen{certMod: c.ModTime(), keyMod: k.ModTime(), certSize: c.Size(), keySize: k.Size()}, nil
}

// GetCertificate is tls.Config's: the certificate now, the files looked at first when every has
// passed since they last were.
func (r *certReloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if now := time.Now(); now.Sub(r.checked) >= r.every {
		r.checked = now
		r.reload()
	}
	return r.cert, nil
}

// reload reads the pair again when the files changed; r.mu is held.
func (r *certReloader) reload() {
	seen, err := r.stat()
	if err == nil && seen == r.seen {
		return
	}
	if err == nil {
		c, lerr := tls.LoadX509KeyPair(r.certFile, r.keyFile)
		r.seen = seen // the same files are not read again until they change
		if lerr == nil {
			r.cert, r.failed = &c, ""
			logger.Info(r.log, "http: the TLS certificate's files changed: serving the new certificate"+leafOf(&c))
			return
		}
		err = fmt.Errorf("http.tls_cert, http.tls_key: %w", lerr)
	}
	if msg := err.Error(); msg != r.failed {
		r.failed = msg
		logger.Error(r.log, err, "http: the TLS certificate's files changed and don't load: the previous certificate is still served")
	}
}

// leafOf names the certificate's leaf for the log: its names and when it expires.
func leafOf(c *tls.Certificate) string {
	if c.Leaf == nil {
		return ""
	}
	names := c.Leaf.DNSNames
	for _, ip := range c.Leaf.IPAddresses {
		names = append(names, ip.String())
	}
	return fmt.Sprintf(" (%s, until %s)", strings.Join(names, ", "), c.Leaf.NotAfter.UTC().Format(time.RFC3339))
}
