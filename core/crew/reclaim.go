package crew

import (
	"context"
	"errors"
	"time"
)

// The daemon's own look for orphaned helpers (docs/decisions/crew.md#crew-bridges): a user shouldn't
// need to understand the crew's infrastructure to keep the machine clean, so a helper the crew
// started that stays orphaned is ended by the daemon itself.
const (
	ReclaimEvery = time.Minute      // the daemon looks this often, with no window open
	reclaimAfter = 10 * time.Minute // orphaned, for the same reason, in every look this long
)

// Reclaimed is one helper the daemon's look ended (Reclaim), or tried to.
type Reclaimed struct {
	Row     BridgeRow
	Outcome string // term | kill | gone
	Err     error  // the kill's error, if it failed
}

// reclaimKey names an orphan across looks: its process (ProcRef) and why it is one. A row whose
// reason changes is another key, so its clock starts over.
func reclaimKey(r BridgeRow) string { return r.Start + "\x00" + r.Why }

// Reclaim is one look of the daemon's, at now: every helper of the crew's own kinds that was found
// orphaned, for the same reason, in every look since at least reclaimAfter ago is ended (killHelper,
// re-checked as it is signalled). A live row, or the shared daemon (foreign), never is. A row seen
// live in a look, gone from it, or orphaned for another reason starts over. What was ended is
// returned for the caller to journal.
func (m *Manager) Reclaim(ctx context.Context, now time.Time) ([]Reclaimed, error) {
	rows, err := m.Bridges(ctx)
	if err != nil {
		return nil, err
	}
	m.bridgeMu.Lock()
	seen := make(map[string]time.Time, len(rows))
	var due []BridgeRow
	for _, r := range rows {
		if r.State != BridgeOrphan {
			continue // forgotten: a live row restarts the clock, a foreign one never has one
		}
		k := reclaimKey(r)
		first, ok := m.bridgeSeen[k]
		if !ok {
			first = now
		}
		seen[k] = first
		if now.Sub(first) >= reclaimAfter {
			due = append(due, r)
		}
	}
	m.bridgeSeen = seen
	m.bridgeMu.Unlock()

	var out []Reclaimed
	for _, r := range due {
		res, err := m.killHelper(ctx, r.PID, r.Start, func(again BridgeRow) error {
			if again.State != BridgeOrphan || again.Why != r.Why {
				return errReclaimChanged
			}
			return nil
		})
		if errors.Is(err, errReclaimChanged) {
			continue // it came back, or its reason changed, between the look and the kill
		}
		out = append(out, Reclaimed{Row: r, Outcome: res.Outcome, Err: err})
		if err == nil {
			m.bridgeMu.Lock()
			delete(m.bridgeSeen, reclaimKey(r))
			m.bridgeMu.Unlock()
		}
	}
	return out, nil
}

// errReclaimChanged is a due orphan that was no longer one, for the same reason, when it was opened.
var errReclaimChanged = errors.New("crew: the orphan changed before its reclaim")
