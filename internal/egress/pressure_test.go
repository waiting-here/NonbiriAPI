package egress

import "testing"

func TestGlobalPressureRetainsShortConcurrencyBurst(t *testing.T) {
	gate, err := NewGate(ConcurrencyLimits{Global: 4, PerEndpoint: 4})
	if err != nil {
		t.Fatal(err)
	}
	first, err := gate.Acquire(t.Context(), "https://upstream.example/v1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := gate.Acquire(t.Context(), "https://upstream.example/v1")
	if err != nil {
		t.Fatal(err)
	}
	second.Release()
	current, peak, limit := gate.SampleGlobal()
	if current != 1 || peak != 2 || limit != 4 {
		t.Fatal(current, peak, limit)
	}
	first.Release()
	if err := gate.SetLimits(ConcurrencyLimits{Global: 1, PerEndpoint: 1}); err != nil {
		t.Fatal(err)
	}
	current, peak, limit = gate.SampleGlobal()
	if current != 0 || peak != 1 || limit != 1 {
		t.Fatal(current, peak, limit)
	}
	_, peak, _ = gate.SampleGlobal()
	if peak != 0 {
		t.Fatal("interval peak did not reset", peak)
	}
}
