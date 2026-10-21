package logapi

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/usageinfo"
)

type LogModalityTokens struct {
	Text  string `json:"text"`
	Image string `json:"image"`
	Audio string `json:"audio"`
	Video string `json:"video"`
}

type LogModalityInput struct {
	UncachedInput   LogModalityTokens `json:"uncached_input"`
	CacheWriteInput LogModalityTokens `json:"cache_write_input"`
	CacheReadInput  LogModalityTokens `json:"cache_read_input"`
}

type LogModalities struct {
	Input          *LogModalityInput  `json:"input,omitempty"`
	Output         *LogModalityTokens `json:"output,omitempty"`
	InputFallback  string             `json:"input_fallback,omitempty"`
	OutputFallback string             `json:"output_fallback,omitempty"`
}

type LogPricing = usageinfo.Pricing

func addUsageDetails(result LogUsage, uncached, cacheWrite, cacheRead, output int64, encoded string) (LogUsage, error) {
	usage, err := usageinfo.Apply(contract.Usage{Present: !result.UsageUnknown, UncachedInputTokens: uncached, CacheWriteInputTokens: cacheWrite, CacheReadInputTokens: cacheRead, OutputTokens: output}, encoded)
	if err != nil {
		return LogUsage{}, ErrInvariant
	}
	pricing, err := usageinfo.PricingFromUsage(encoded)
	if err != nil {
		return LogUsage{}, ErrInvariant
	}
	if pricing != (usageinfo.Pricing{}) {
		result.Pricing = &pricing
	}
	if usage.SearchUnitsPresent {
		value := strconv.FormatInt(usage.SearchUnitsMicros/1000000, 10)
		if fraction := usage.SearchUnitsMicros % 1000000; fraction != 0 {
			value += "." + strings.TrimRight(fmt.Sprintf("%06d", fraction), "0")
		}
		result.SearchUnits = &value
	}
	// Legacy rows have no classification metadata to display.
	if usage.Present && encoded != "" && strings.TrimSpace(encoded) != "{}" {
		m := usage.Modalities
		result.Modalities = &LogModalities{InputFallback: m.InputReason, OutputFallback: m.OutputReason}
		if m.InputPresent {
			result.Modalities.Input = &LogModalityInput{UncachedInput: modalityTokens(m.Input[0]), CacheWriteInput: modalityTokens(m.Input[1]), CacheReadInput: modalityTokens(m.Input[2])}
		}
		if m.OutputPresent {
			out := modalityTokens(m.Output)
			result.Modalities.Output = &out
		}
	}
	return result, nil
}

func modalityTokens(values [4]int64) LogModalityTokens {
	return LogModalityTokens{Text: strconv.FormatInt(values[0], 10), Image: strconv.FormatInt(values[1], 10), Audio: strconv.FormatInt(values[2], 10), Video: strconv.FormatInt(values[3], 10)}
}

func usageCSV(usage LogUsage) []string {
	values := []string{csvString(usage.SearchUnits), "", ""}
	if usage.Modalities != nil {
		encoded, _ := json.Marshal(usage.Modalities)
		values[1] = string(encoded)
	}
	if usage.Pricing != nil {
		encoded, _ := json.Marshal(usage.Pricing)
		values[2] = string(encoded)
	}
	return values
}
