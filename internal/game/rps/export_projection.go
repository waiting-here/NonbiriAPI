package rps

type CurrentExportProjection struct {
	Kind       string  `json:"kind"`
	ResourceID string  `json:"resource_id"`
	Mode       string  `json:"mode"`
	State      string  `json:"state"`
	Phase      *string `json:"phase"`
	Deadline   *int64  `json:"deadline"`
}

func ProjectExportCurrent(value *HomeState) (*CurrentExportProjection, error) {
	if value == nil {
		return nil, nil
	}
	switch value.Kind {
	case "queue":
		if value.Queue == nil || value.Session != nil || value.Result != nil {
			return nil, ErrInvariant
		}
		deadline := value.Queue.Deadline
		return &CurrentExportProjection{Kind: "queue", ResourceID: value.Queue.ID, Mode: value.Queue.Mode, State: value.Queue.State, Deadline: &deadline}, nil
	case "session":
		if value.Session == nil || value.Queue != nil || value.Result != nil {
			return nil, ErrInvariant
		}
		phase := value.Session.Phase
		out := &CurrentExportProjection{Kind: "session", ResourceID: value.Session.SessionID, Mode: value.Session.Mode, State: value.Session.State, Phase: &phase}
		if value.Session.Deadline != nil {
			deadline := *value.Session.Deadline
			out.Deadline = &deadline
		}
		return out, nil
	default:
		return nil, ErrInvariant
	}
}
