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

// ProbeInfo is what an AutoDoc daemon says of itself to a probe: a hello that declares no protocol.
type ProbeInfo struct {
	Version     string
	Protocol    int64 // the daemon's own protocol
	MinProtocol int64 // the oldest it serves; equal to Protocol for a daemon before ranges
	StoreID     string
	Instance    string
	Addr        string
}

// Probe asks the occupant of addr who it is, with a sys.hello that declares no protocol (so it
// admits nothing), and returns what any AutoDoc answers, whatever its protocol: the caller decides
// whether it will do. A dial error is returned as it is: nothing answers there.
func Probe(ctx context.Context, network, addr string) (ProbeInfo, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, network, addr)
	if err != nil {
		return ProbeInfo{}, err
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
		return ProbeInfo{}, err
	}
	if err := bw.Flush(); err != nil {
		return ProbeInfo{}, fmt.Errorf("%w: %v", ErrNotAutodoc, err)
	}
	m, err := codec.Read(bufio.NewReader(conn))
	switch {
	case err != nil:
		return ProbeInfo{}, fmt.Errorf("%w: %v", ErrNotAutodoc, err)
	case m.Kind != golibrpc.KindResponse || m.ID != probeID || m.Err != nil:
		return ProbeInfo{}, fmt.Errorf("%w: not a hello answer", ErrNotAutodoc)
	}
	result, _ := m.Result.(map[string]any)
	if name, _ := result["server"].(string); name != ServerName {
		return ProbeInfo{}, ErrNotAutodoc
	}
	var info ProbeInfo
	var ok bool
	if info.Protocol, ok = result["protocol"].(int64); !ok {
		return ProbeInfo{}, fmt.Errorf("%w: no protocol", ErrNotAutodoc)
	}
	if info.Version, ok = result["version"].(string); !ok {
		return ProbeInfo{}, fmt.Errorf("%w: no version", ErrNotAutodoc)
	}
	info.MinProtocol = info.Protocol
	if mp, ok := result["min_protocol"].(int64); ok {
		info.MinProtocol = mp
	}
	info.StoreID, _ = result["store_id"].(string)
	info.Instance, _ = result["instance"].(string)
	info.Addr, _ = result["addr"].(string)
	return info, nil
}

// ProbeOn asks the occupant of addr who it is, as Probe does, and returns its version when it is an
// AutoDoc of this Protocol. A dial error is returned as it is: nothing answers there.
func ProbeOn(ctx context.Context, network, addr string) (string, error) {
	info, err := Probe(ctx, network, addr)
	if err != nil {
		return "", err
	}
	if info.Protocol != Protocol {
		return "", fmt.Errorf("%w: protocol %d, want %d", ErrNotAutodoc, info.Protocol, Protocol)
	}
	return info.Version, nil
}
