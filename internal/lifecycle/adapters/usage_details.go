package adapters

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/charity"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

func consumerUsageDetails(value charity.ConsumerExport) *lifecycle.UsageDetailsExport {
	if !value.UsageDetailsPresent {
		return nil
	}
	u := value.UsageDetails
	out := &lifecycle.UsageDetailsExport{}
	if u.SearchUnitsPresent {
		units := strconv.FormatInt(u.SearchUnitsMicros/1000000, 10)
		if fraction := u.SearchUnitsMicros % 1000000; fraction != 0 {
			units += "." + strings.TrimRight(fmt.Sprintf("%06d", fraction), "0")
		}
		out.SearchUnits = &units
	}
	if u.Present {
		m := u.Modalities
		out.Modalities = &lifecycle.ModalityUsageExport{InputFallback: m.InputReason, OutputFallback: m.OutputReason}
		if m.InputPresent {
			out.Modalities.Input = &lifecycle.ModalityInputExport{UncachedInput: exportModalityTokens(m.Input[0]), CacheWriteInput: exportModalityTokens(m.Input[1]), CacheReadInput: exportModalityTokens(m.Input[2])}
		}
		if m.OutputPresent {
			tokens := exportModalityTokens(m.Output)
			out.Modalities.Output = &tokens
		}
	}
	return out
}

func exportModalityTokens(v [4]int64) lifecycle.ModalityTokensExport {
	return lifecycle.ModalityTokensExport{Text: strconv.FormatInt(v[0], 10), Image: strconv.FormatInt(v[1], 10), Audio: strconv.FormatInt(v[2], 10), Video: strconv.FormatInt(v[3], 10)}
}
