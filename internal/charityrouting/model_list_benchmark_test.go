package charityrouting

import "testing"

// Model discovery evaluates the live donation limits for every visible model.
// Exercise the read pool under overlapping client refreshes as well as one caller.
func BenchmarkListAvailableModels(b *testing.B) {
	env := newBrowseProjectionFixture(b, 327, 16)
	read := func(b *testing.B) error {
		models, err := env.service.ListAvailableModels(b.Context(), env.caller, routingTestNow, MaxAvailableModels)
		if err == nil && len(models) != 246 {
			b.Errorf("got %d available models, want 246", len(models))
		}
		return err
	}
	b.Run("single", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if err := read(b); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("concurrent", func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				if err := read(b); err != nil {
					b.Error(err)
					return
				}
			}
		})
	})
}
