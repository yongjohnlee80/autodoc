package crew

import "context"

// Presences, as crew.list reports them (§4.4).
const (
	PresenceLive         = "live"         // its host renewed within the lease
	PresenceReconnecting = "reconnecting" // the daemon is younger than a lease: hosts are still coming back
	PresenceOffline      = "offline"      // its host's lease lapsed
	PresenceEnded        = "ended"        // no live session
)

// unreachableAfter is how many consecutive failed wakes make a session unreachable (§4.7).
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
				_, hasEndpoint, err := tx.Endpoint(s.ID)
				if err != nil {
					return err
				}
				w, _, err := tx.Wake(s.ID)
				if err != nil {
					return err
				}
				e.Reachable = hasEndpoint && w.Failures < unreachableAfter
			}
			out = append(out, e)
		}
		return nil
	})
	return out, err
}
