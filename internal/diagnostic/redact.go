package diagnostic

// Redact applies the reader's existing display policy to untrusted text while
// retaining measured counters and locally authored classifications.
func Redact(f *Failure, project func(string) string) {
	if f == nil {
		return
	}
	f.Message = project(f.Message)
	for i := range f.Causes {
		f.Causes[i].Message = project(f.Causes[i].Message)
	}
	for i := range f.Secondary {
		f.Secondary[i].Message = project(f.Secondary[i].Message)
	}
	for i := range f.Correlation {
		f.Correlation[i].Value = project(f.Correlation[i].Value)
	}
}
