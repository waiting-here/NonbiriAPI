package imageactivity

import (
	"strconv"
	"strings"
)

const maxDimension = 65536

func (r DimensionRule) validate() error {
	if r.Format != "width_height" {
		return ErrInvalid
	}
	for _, axis := range []DimensionRange{r.Width, r.Height} {
		if axis.Minimum < 1 || axis.Maximum > maxDimension || axis.Minimum > axis.Maximum || axis.Step < 1 || axis.Step > maxDimension {
			return ErrInvalid
		}
	}
	return nil
}

func (r DimensionRule) checkValue(value string) error {
	if r.validate() != nil {
		return ErrInvalid
	}
	width, height, ok := strings.Cut(value, "x")
	if !ok {
		return ErrInvalid
	}
	for i, text := range []string{width, height} {
		if len(text) < 1 || len(text) > 5 {
			return ErrInvalid
		}
		n, err := strconv.Atoi(text)
		axis := r.Width
		if i == 1 {
			axis = r.Height
		}
		if err != nil || strconv.Itoa(n) != text || n < axis.Minimum || n > axis.Maximum || (n-axis.Minimum)%axis.Step != 0 {
			return ErrInvalid
		}
	}
	return nil
}
