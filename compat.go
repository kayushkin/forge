package forge

// v2 compat shim — bridges old bus-agent forge.go to v3 API

// Slot is a v2 compatibility type. Maps to SlotV3 internally.
type Slot struct {
	ID        int
	Project   string
	Path      string
	Status    string
	AgentID   string
	SessionID string
	Branch    string
}

// Acquire is a v2 compat wrapper. Finds an idle slot and marks it active.
func (f *Forge) Acquire(projectID string, opts AcquireOpts) (*Slot, error) {
	info, err := f.OpenSlot(projectID, opts.SessionID, opts.AgentID)
	if err != nil {
		return nil, err
	}
	return &Slot{
		ID:        info.ID,
		Project:   projectID,
		Status:    "active",
		AgentID:   opts.AgentID,
		SessionID: opts.SessionID,
	}, nil
}

// Release is a v2 compat wrapper.
func (f *Forge) Release(projectID string, slotID int) error {
	// Force close (remove agents + set idle)
	return f.ForceCloseSlot(slotID)
}

// CleanSlot is a v2 compat stub (no-op in containerized staging).
func (f *Forge) CleanSlot(projectID string, slotID int) error {
	return nil
}

// SlotPull is a v2 compat stub (no-op in containerized staging).
func (f *Forge) SlotPull(projectID string, slotID int) error {
	return nil
}

// SlotStatus returns v2-style slot status for a project.
func (f *Forge) SlotStatus(projectID string) ([]Slot, error) {
	slots, err := f.ListProjectSlotsV3(projectID)
	if err != nil {
		return nil, err
	}
	var result []Slot
	for _, s := range slots {
		result = append(result, Slot{
			ID:        s.ID,
			Project:   s.ProjectID,
			Status:    s.Status,
			AgentID:   s.AgentID,
			SessionID: s.SessionID,
		})
	}
	return result, nil
}

// Deploy, RecordDeploy, FinishDeploy, ListDeploys, AllDeploys are in deploy.go
