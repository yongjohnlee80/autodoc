package rpc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/yongjohnlee80/golib/msgpack"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"
)

// ErrNotAutodoc is an endpoint whose occupant is not a compatible AutoDoc.
var ErrNotAutodoc = errors.New("rpc: the endpoint's occupant is not a compatible autodoc")

// probeLimits bound the occupant's answer: a hello, and nothing larger.
func probeLimits() *msgpack.Limits {
	return &msgpack.Limits{MaxDepth: 4, MaxStrBytes: 256, MaxBinBytes: 256, MaxElements: 16,
		MaxTotalElements: 64, MaxTotalBytes: 1 << 10}
}

// ProbeOn asks the occupant of addr who it is, with a sys.hello that declares no protocol (so it
// admits nothing), and returns its version when it is an AutoDoc of this Protocol. A dial error is
// returned as it is: nothing answers there.
func ProbeOn(ctx context.Context, network, addr string) (string, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, network, addr)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	}
	codec := msgpackrpc.New(probeLimits())
	const probeID = 1
	bw := bufio.NewWriter(conn)
	if err := codec.Write(bw, &golibrpc.Message{Kind: golibrpc.KindRequest, ID: probeID, Method: "sys.hello",
		Params: []any{map[string]any{}}}); err != nil {
		return "", err
	}
	if err := bw.Flush(); err != nil {
		return "", fmt.Errorf("%w: %v", ErrNotAutodoc, err)
	}
	m, err := codec.Read(bufio.NewReader(conn))
	switch {
	case err != nil:
		return "", fmt.Errorf("%w: %v", ErrNotAutodoc, err)
	case m.Kind != golibrpc.KindResponse || m.ID != probeID || m.Err != nil:
		return "", fmt.Errorf("%w: not a hello answer", ErrNotAutodoc)
	}
	result, _ := m.Result.(map[string]any)
	if name, _ := result["server"].(string); name != ServerName {
		return "", ErrNotAutodoc
	}
	if proto, ok := result["protocol"].(int64); !ok || proto != Protocol {
		return "", fmt.Errorf("%w: protocol %v, want %d", ErrNotAutodoc, result["protocol"], Protocol)
	}
	ver, ok := result["version"].(string)
	if !ok {
		return "", fmt.Errorf("%w: no version", ErrNotAutodoc)
	}
	return ver, nil
}
