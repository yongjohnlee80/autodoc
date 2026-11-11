package client

import (
	"context"
	"errors"
	"fmt"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
)

// THE CALLS KEPT APART: a session may name calls that wait long on the daemon by design, its
// long-polls, and those go on a second connection of its own (the polling connection), so they
// never take the handler slots its other calls are read in: the daemon reads at most a fixed
// number of a connection's requests at once (golib's MaxConcurrent), and a request past them is
// not read until one ends. The polling connection says hello as the primary did, at the
// protocol the primary was admitted at, to the same daemon (its instance) serving the same store;
// it is dialed on the first call kept apart, lives as long as the primary it was dialed under, and
// ends with it. The primary alone is the session's: its generation, its Done, its client token
// and its event head are what the session answers, whatever the polling connection does.

// errAnotherDaemon is a polling connection whose hello came from another daemon, store or
// protocol than the primary's.
var errAnotherDaemon = errors.New("the polling connection reached another daemon than the session's")

// Apart has the session send each of verbs on its polling connection rather than its primary one.
// Set before the first Connect; it is read without the session's mutex.
func (s *Session) Apart(verbs ...string) *Session {
	s.apart = map[string]bool{}
	for _, v := range verbs {
		s.apart[v] = true
	}
	return s
}

// pollClient is the polling connection for the primary there is now, dialed if there is none:
// one dial at a time, a caller arriving during it waiting for its end (or for ctx). The dial and
// its hello end with the primary they are for: a Connect, a Close or the primary's own end cancels
// them, and a connection dialed for a primary that has since gone is closed, never published.
func (s *Session) pollClient(ctx context.Context) (*golibrpc.Client, error) {
	for {
		s.mu.Lock()
		primary, gen := s.client, s.gen
		if primary == nil {
			s.mu.Unlock()
			return nil, errNotConnected
		}
		if s.poll != nil && s.pollGen == gen {
			cli := s.poll
			s.mu.Unlock()
			return cli, nil
		}
		if f := s.pollDialing; f != nil {
			s.mu.Unlock()
			select {
			case <-f.done:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		dctx, cancel := context.WithCancel(ctx)
		flight := &pollFlight{done: make(chan struct{}), cancel: cancel}
		s.pollDialing = flight
		want := pollHello{instance: s.instance, pid: s.pid, storeID: s.storeID, protocol: s.proto}
		s.mu.Unlock()
		go func() { // the primary ending ends the dial for it
			select {
			case <-primary.Done():
				cancel()
			case <-dctx.Done():
			}
		}()

		cli, err := s.dialPoll(dctx, want)
		cancel() // the dial and hello are over: the connection, if any, outlives their ctx

		s.mu.Lock()
		if s.pollDialing == flight {
			s.pollDialing = nil
		}
		close(flight.done)
		if s.gen != gen || s.client != primary || isDone(primary) {
			err = errNotConnected // the primary it was dialed for is gone: a newer one dials its own
		}
		if err != nil {
			s.mu.Unlock()
			if cli != nil {
				_ = cli.Close()
			}
			return nil, err
		}
		s.poll, s.pollGen = cli, gen
		s.mu.Unlock()
		go s.endPollWith(primary, cli)
		return cli, nil
	}
}

// pollFlight is a polling connection's dial in flight: done closes when it ends, and cancel ends
// its dial and hello.
type pollFlight struct {
	done   chan struct{}
	cancel context.CancelFunc
}

// pollHello is what the polling connection's hello must answer: the primary's.
type pollHello struct {
	instance string
	pid      int64
	storeID  string
	protocol int64
}

// dialPoll dials a polling connection and says hello on it as the primary did. It answers a
// connection only when its hello names the primary's daemon, store and protocol.
func (s *Session) dialPoll(ctx context.Context, want pollHello) (*golibrpc.Client, error) {
	cli, err := s.dial(ctx)
	if err != nil {
		return nil, err
	}
	res, err := cli.Call(ctx, "sys.hello", s.helloInfo(want.protocol))
	if err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("the polling connection's hello: %w", err)
	}
	m, _ := res.(map[string]any)
	instance, _ := m["instance"].(string)
	pid, _ := m["pid"].(int64)
	storeID, _ := m["store_id"].(string)
	protocol, _ := m["protocol"].(int64)
	if instance != want.instance || pid != want.pid || storeID != want.storeID || protocol != want.protocol {
		_ = cli.Close()
		return nil, fmt.Errorf("%w (instance %q pid %d store %q protocol %d; the primary's %q %d %q %d)", errAnotherDaemon,
			instance, pid, storeID, protocol, want.instance, want.pid, want.storeID, want.protocol)
	}
	return cli, nil
}

// endPollWith closes the polling connection cli when the primary ends, or when cli itself does:
// either way it is the session's no longer, and the next call kept apart dials afresh.
func (s *Session) endPollWith(primary, cli *golibrpc.Client) {
	select {
	case <-primary.Done():
	case <-cli.Done():
	}
	s.dropPoll(cli)
}

// dropPoll closes cli, and forgets it when it is the session's polling connection.
func (s *Session) dropPoll(cli *golibrpc.Client) {
	s.mu.Lock()
	if s.poll == cli {
		s.poll = nil
	}
	s.mu.Unlock()
	_ = cli.Close()
}

// takePoll forgets the polling connection, answering it for its caller to close, and cancels a
// dial of one in flight: a Connect or a Close ends both with the primary. Called with s.mu held.
func (s *Session) takePoll() *golibrpc.Client {
	if f := s.pollDialing; f != nil {
		f.cancel()
		s.pollDialing = nil
	}
	cli := s.poll
	s.poll = nil
	return cli
}

// callApart is Call for a verb kept apart: on the polling connection. A call failed by its
// connection's end drops that connection, so the next one dials again.
func (s *Session) callApart(ctx context.Context, method string, params ...any) (any, error) {
	cli, err := s.pollClient(ctx)
	if err != nil {
		return nil, err
	}
	res, err := cli.Call(ctx, method, params...)
	if err != nil && isDone(cli) {
		s.dropPoll(cli)
	}
	return res, err
}

// isDone reports whether cli has ended.
func isDone(cli *golibrpc.Client) bool {
	select {
	case <-cli.Done():
		return true
	default:
		return false
	}
}
