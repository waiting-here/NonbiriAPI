package antiabuse

import "testing"

type mapConfigReader map[string]string

func (r mapConfigReader) GetSiteConfigValue(key string) (string, error) {
	return r[key], nil
}

func TestReadConfigIntFieldsStayWithinPortableBounds(t *testing.T) {
	cfg := readConfig(mapConfigReader{
		KeyRPMBanThreshold:              "4096",
		KeyCharityMinChars:              "1048576",
		KeyCharityViolationBanThreshold: "4096",
		KeyCharitySuspendThreshold:      "4096",
	})
	if cfg.RPMBanThreshold != MaxViolationThreshold {
		t.Fatalf("rpm threshold = %d, want %d", cfg.RPMBanThreshold, MaxViolationThreshold)
	}
	if cfg.CharityMinChars != MaxCharityContentRuneCount {
		t.Fatalf("charity minimum = %d, want %d", cfg.CharityMinChars, MaxCharityContentRuneCount)
	}
	if cfg.CharityViolationBanThreshold != MaxViolationThreshold {
		t.Fatalf("violation threshold = %d, want %d", cfg.CharityViolationBanThreshold, MaxViolationThreshold)
	}
	if cfg.CharitySuspendThreshold != MaxViolationThreshold {
		t.Fatalf("suspend threshold = %d, want %d", cfg.CharitySuspendThreshold, MaxViolationThreshold)
	}

	corrupt := readConfig(mapConfigReader{
		KeyRPMBanThreshold:              "4294967296",
		KeyCharityMinChars:              "4294967296",
		KeyCharityViolationBanThreshold: "-1",
		KeyCharitySuspendThreshold:      "4097",
	})
	defaults := DefaultConfig()
	if corrupt.RPMBanThreshold != defaults.RPMBanThreshold {
		t.Fatalf("corrupt rpm threshold = %d, want fallback %d", corrupt.RPMBanThreshold, defaults.RPMBanThreshold)
	}
	if corrupt.CharityMinChars != defaults.CharityMinChars {
		t.Fatalf("corrupt charity minimum = %d, want fallback %d", corrupt.CharityMinChars, defaults.CharityMinChars)
	}
	if corrupt.CharityViolationBanThreshold != defaults.CharityViolationBanThreshold {
		t.Fatalf("corrupt violation threshold = %d, want fallback %d", corrupt.CharityViolationBanThreshold, defaults.CharityViolationBanThreshold)
	}
	if corrupt.CharitySuspendThreshold != defaults.CharitySuspendThreshold {
		t.Fatalf("corrupt suspend threshold = %d, want fallback %d", corrupt.CharitySuspendThreshold, defaults.CharitySuspendThreshold)
	}
}
