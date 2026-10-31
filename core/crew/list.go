package crew

import "context"

// Presences, as crew.list reports them (docs/decisions/crew.md#sessions-and-the-guard).
const (
	PresenceLive         = "live"         // its host renewed within the lease
	PresenceReconnecting = "reconnecting" // the daemon is younger than a lease: hosts are still coming back
	PresenceOffline      = "offline"      // its host's lease lapsed
	PresenceEnded        = "ended"        // no live session
)

// unreachableAfter is how many consecutive failed wakes make a session unreachable
// (docs/decisions/crew.md#wakers-and-the-wake-scheduler).
const unreachableAfter = 5

// Entry is one row of the addressbook.
type Entry struct {
	Codename  string `json:"codename"`
	Scope     string `json:"scope"`
	Kind      string `json:"kind"`
	Presence  string `json:"presence"`
	Reachable bool   `json:"reachable"` // a live session with an endpoint and fewer than 5 failed wakes
}

// List is the addressbook (crew.list): every active profile, by scope then codename, with its
// presence and whether a wake can reach it.
func (m *Manager) List(ctx context.Context) ([]Entry, error) {
	var out []Entry
	err := m.store.Read(ctx, func(tx Tx) error {
		now := tx.Now()
		profiles, err := tx.ActiveProfiles()
		if err != nil {
			return err
		}
		for _, p := range profiles {
			e := Entry{Codename: p.Codename, Scope: p.Scope, Kind: p.Kind, Presence: PresenceEnded}
			s, live, err := tx.LiveSession(p.ID)
			if err != nil {
				return err
			}
			if live {
				h, _, err := tx.Host(s.Host)
				if err != nil {
					return err
				}
				switch {
				case now.UnixNano()-h.Renewed <= int64(leaseTTL):
					e.Presence = PresenceLive
				case now.Sub(m.started) < leaseTTL:
					e.Presence = PresenceReconnecting
				default:
					e.Presence = PresenceOffline
				}
				if e.Reachable, err = sessionReachable(tx, s.ID); err != nil {
					return err
				}
			}
			out = append(out, e)
		}
		return nil
	})
	return out, err
}

// Reachable reports whether profile's live session can be woken now, in tx: crew.list's reachable,
// for another module's write (the user's sends from a window, which type the text instead when it
// can't be).
func (m *Manager) Reachable(tx Tx, profile string) (bool, error) {
	s, live, err := tx.LiveSession(profile)
	if err != nil || !live {
		return false, err
	}
	return sessionReachable(tx, s.ID)
}

// sessionReachable is whether live session id can be woken: it has an endpoint, and fewer than
// unreachableAfter consecutive failed wakes.
func sessionReachable(tx Tx, id string) (bool, error) {
	_, hasEndpoint, err := tx.Endpoint(id)
	if err != nil || !hasEndpoint {
		return false, err
	}
	w, _, err := tx.Wake(id)
	return w.Failures < unreachableAfter, err
}
