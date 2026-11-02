package todo

import "context"

// AssignedBy is who made an assignment a consumer read (docs/decisions/todos.md#an-assignment-says-who-made-it),
// as the change log recorded it: the assign change seq of store (task, when given, must be its task),
// or, for seq 0 (a resync's row), the snapshot row of task in store, while task has an assignee. by is
// "" for the user's surface. found is false when there is no such assignment: a change pruned or not an
// assign, a task gone or unassigned.
func (m *Manager) AssignedBy(ctx context.Context, store string, seq int64, task string) (by string, found bool, err error) {
	if seq > 0 {
		cs, err := m.j.After(ctx, store, seq-1, 1)
		if err != nil || len(cs) == 0 {
			return "", false, err
		}
		c := cs[0]
		if c.Seq != seq || c.Kind != KindAssign || task != "" && c.ID != task {
			return "", false, nil
		}
		return c.By, true, nil
	}
	if task == "" {
		return "", false, nil
	}
	rows, err := m.j.Snapshot(ctx, store)
	if err != nil {
		return "", false, err
	}
	for _, r := range rows {
		if r.ID == task && r.Assignee != "" {
			return r.By, true, nil
		}
	}
	return "", false, nil
}
