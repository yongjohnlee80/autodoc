//go:build server

package app

import (
	"net"
	"net/http"
	"strings"
	"testing"
)

// TestTheAPIReadsASmallHeader: the API's server reads at most maxHeaderBytes of a request's header,
// so anyone's request costs it little: a header past it is 431, one within it is served.
func TestTheAPIReadsASmallHeader(t *testing.T) {
	srv := newHTTPServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	get := func(header int) int {
		req, err := http.NewRequest(http.MethodGet, "http://"+ln.Addr().String()+"/v1/health", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Pad", strings.Repeat("a", header))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if got := get(1 << 10); got != http.StatusNoContent {
		t.Errorf("a 1 KiB header: %d", got)
	}
	if got := get(64 << 10); got != http.StatusRequestHeaderFieldsTooLarge {
		t.Errorf("a 64 KiB header: %d, want 431", got)
	}
}
