package crew

// RaisePending raises pending_gen for profile's live session in tx, creating its wake row when it
// has none, so the scheduler wakes it for the delivery that just became eligible
// (docs/decisions/crew.md#the-crew-and-mail-tables). A profile with no live session is no error:
// crew.register seeds pending_gen for the next one (docs/decisions/crew.md#sessions-and-the-guard).
func (m *Manager) RaisePending(tx Tx, profile string) error {
	s, ok, err := tx.LiveSession(profile)
	if err != nil || !ok {
		return err
	}
	w, ok, err := tx.Wake(s.ID)
	if err != nil {
		return err
	}
	if !ok {
		w = Wake{Session: s.ID, State: WakeIdle}
	}
	w.PendingGen++
	return tx.PutWake(w)
}
