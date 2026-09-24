package imageactivity

import (
	"encoding/json"
	"errors"
)

func receiptIndicator(raw []byte) (any, error) {
	if len(raw) == 0 || len(raw) > 128*6+2 {
		return nil, ErrInvalid
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil, ErrInvalid
	}
	switch v := value.(type) {
	case bool:
		return v, nil
	case string:
		if len(v) != 0 && safeText(v, 128) {
			return v, nil
		}
	}
	return nil, ErrInvalid
}

func validateReceipt(a Adapter) error {
	r := a.Submit.Receipt
	if r == nil {
		return nil
	}
	if a.Poll == nil || a.Response.TaskIDPointer == nil || a.Response.StatePointer == nil {
		return ErrInvalid
	}
	if _, err := receiptIndicator(r.IndicatorValue); err != nil {
		return err
	}
	paths := [][]string{}
	for _, pointer := range []string{r.IndicatorPointer, *a.Response.TaskIDPointer, a.Response.ImagesPointer, *a.Response.StatePointer} {
		parts, err := pointerParts(pointer, false)
		if err != nil {
			return err
		}
		for _, previous := range paths {
			shared := min(len(parts), len(previous))
			overlap := true
			for i := range shared {
				if parts[i] != previous[i] {
					overlap = false
					break
				}
			}
			if overlap {
				return ErrInvalid
			}
		}
		paths = append(paths, parts)
	}
	return nil
}

// submissionState permits explicitly configured receipt markers without changing
// the status grammar used by later polls. Conflicting facts remain uncertain.
func submissionState(body []byte, a Adapter) (string, error) {
	r := a.Submit.Receipt
	if r == nil {
		return responseState(body, a.Response)
	}
	if validateReceipt(a) != nil || len(body) > maxResponse || !json.Valid(body) {
		return "", ErrInvalid
	}
	if end, err := valueEnd(body, 0, 0); err != nil || whitespace(body, end) != len(body) {
		return "", ErrInvalid
	}
	expected, _ := receiptIndicator(r.IndicatorValue)
	matched := false
	marker, err := rawPointer(body, r.IndicatorPointer, false)
	if err == nil {
		value, e := receiptIndicator(marker)
		if e != nil {
			return "", e
		}
		// A different JSON type is not an explicit negative receipt marker.
		switch expected.(type) {
		case bool:
			if _, ok := value.(bool); !ok {
				return "", ErrInvalid
			}
		case string:
			if _, ok := value.(string); !ok {
				return "", ErrInvalid
			}
		}
		matched = value == expected
	} else if !errors.Is(err, ErrNotFound) {
		return "", err
	}
	hasID := false
	if _, err = rawPointer(body, *a.Response.TaskIDPointer, false); err == nil {
		if _, err = rawString(body, *a.Response.TaskIDPointer, 2048); err != nil {
			return "", err
		}
		hasID = true
	} else if !errors.Is(err, ErrNotFound) {
		return "", err
	}
	hasImages := false
	images, err := rawPointer(body, a.Response.ImagesPointer, false)
	if err == nil {
		items, e := rawArray(images, 16)
		if e != nil {
			return "", e
		}
		hasImages = len(items) > 0
	} else if !errors.Is(err, ErrNotFound) {
		return "", err
	}
	state := ""
	if _, err = rawPointer(body, *a.Response.StatePointer, false); err == nil {
		state, err = responseState(body, a.Response)
		if err != nil {
			return "", err
		}
	} else if !errors.Is(err, ErrNotFound) {
		return "", err
	}
	if state == "failed" {
		if matched || hasID || hasImages {
			return "", ErrInvalid
		}
		return "failed", nil
	}
	if matched {
		if !hasID || hasImages || state == "succeeded" {
			return "", ErrInvalid
		}
		return "running", nil
	}
	if hasID || state == "running" || !hasImages {
		return "", ErrInvalid
	}
	return "succeeded", nil
}
