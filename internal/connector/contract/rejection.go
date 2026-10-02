package contract

// RequestRejection contains only fixed field names and locally authored reasons.
// Never construct it from a request value, arbitrary field name or upstream text.
type RequestRejection struct {
	Stage  string
	Field  string
	Reason string
}

func (r *RequestRejection) Error() string {
	return r.Stage + ": " + r.Field + ": " + r.Reason
}
