package imageactivity

import (
	"strconv"
	"strings"
)

type SizeMode string

const (
	ResolutionRatioGrid SizeMode = "resolution_ratio_grid"
	RatioSizeMap        SizeMode = "ratio_size_map"
	RatioResolution     SizeMode = "ratio_resolution"
	WidthHeight         SizeMode = "width_height"
)

type SizeCombination struct {
	Ratio      string `json:"ratio,omitempty"`
	Resolution string `json:"resolution,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	Tier       string `json:"tier,omitempty"`
}

type SizeCapability struct {
	Mode         SizeMode          `json:"mode"`
	Combinations []SizeCombination `json:"combinations,omitempty"`
	Width        *DimensionRange   `json:"width,omitempty"`
	Height       *DimensionRange   `json:"height,omitempty"`
	MaxPixels    int64             `json:"max_pixels,omitempty"`
	Auto         bool              `json:"auto,omitempty"`
}

type SizeInput struct {
	Ratio      string `json:"aspect_ratio,omitempty"`
	Resolution string `json:"resolution,omitempty"`
	Size       string `json:"size,omitempty"`
	Auto       bool   `json:"auto,omitempty"`
}

type ResolvedSize struct {
	Values    map[ParameterKey]any `json:"values"`
	Selection PriceSelection       `json:"selection"`
}

func validDimensionRange(r DimensionRange) bool {
	return r.Minimum >= 1 && r.Maximum >= r.Minimum && r.Maximum <= maxDimension && r.Step >= 1 && r.Step <= maxDimension
}

func validSizeLabel(s string) bool { return s != "" && safeText(s, 128) }

func (c SizeCapability) Validate() error {
	if len(c.Combinations) > 2048 || c.MaxPixels < 0 || c.MaxPixels > int64(maxDimension)*int64(maxDimension) {
		return ErrInvalid
	}
	if c.Mode != ResolutionRatioGrid && c.Mode != RatioSizeMap && c.Mode != RatioResolution && c.Mode != WidthHeight {
		return ErrInvalid
	}
	if c.Mode == WidthHeight {
		if c.Width == nil || c.Height == nil || !validDimensionRange(*c.Width) || !validDimensionRange(*c.Height) {
			return ErrInvalid
		}
	} else if c.Width != nil || c.Height != nil || c.MaxPixels != 0 {
		return ErrInvalid
	}
	if c.Mode != WidthHeight && len(c.Combinations) == 0 && !c.Auto {
		return ErrInvalid
	}
	logical := map[string]bool{}
	dimensions := map[[2]int]string{}
	resolutionPresent := false
	resolutionAbsent := false
	for _, row := range c.Combinations {
		if row.Tier != "" && !validSizeLabel(row.Tier) || row.Width < 0 || row.Height < 0 || (row.Width == 0) != (row.Height == 0) || row.Width > maxDimension || row.Height > maxDimension {
			return ErrInvalid
		}
		var key string
		switch c.Mode {
		case ResolutionRatioGrid:
			if !validSizeLabel(row.Ratio) || !validSizeLabel(row.Resolution) || row.Width == 0 {
				return ErrInvalid
			}
			key = row.Ratio + "\x00" + row.Resolution
		case RatioSizeMap:
			if !validSizeLabel(row.Ratio) || row.Resolution != "" || row.Width == 0 {
				return ErrInvalid
			}
			key = row.Ratio
		case RatioResolution:
			if !validSizeLabel(row.Ratio) || row.Resolution != "" && !validSizeLabel(row.Resolution) {
				return ErrInvalid
			}
			resolutionPresent = resolutionPresent || row.Resolution != ""
			resolutionAbsent = resolutionAbsent || row.Resolution == ""
			if resolutionPresent && resolutionAbsent {
				return ErrInvalid
			}
			key = row.Ratio + "\x00" + row.Resolution
		case WidthHeight:
			if row.Ratio != "" || row.Resolution != "" || row.Width == 0 || !c.containsDimensions(row.Width, row.Height) {
				return ErrInvalid
			}
			key = strconv.Itoa(row.Width) + "x" + strconv.Itoa(row.Height)
		}
		if logical[key] {
			return ErrInvalid
		}
		logical[key] = true
		if row.Width > 0 {
			d := [2]int{row.Width, row.Height}
			if _, ok := dimensions[d]; ok {
				return ErrInvalid
			}
			dimensions[d] = row.Tier
		}
	}
	return nil
}

func (c SizeCapability) containsDimensions(width, height int) bool {
	if c.Width == nil || c.Height == nil || width < c.Width.Minimum || width > c.Width.Maximum || height < c.Height.Minimum || height > c.Height.Maximum {
		return false
	}
	if (width-c.Width.Minimum)%c.Width.Step != 0 || (height-c.Height.Minimum)%c.Height.Step != 0 {
		return false
	}
	return c.MaxPixels == 0 || int64(width)*int64(height) <= c.MaxPixels
}

func parseSizePair(raw string) (int, int, bool) {
	width, height, found := strings.Cut(raw, "x")
	if !found || len(width) == 0 || len(width) > 5 || len(height) == 0 || len(height) > 5 {
		return 0, 0, false
	}
	w, errW := strconv.Atoi(width)
	h, errH := strconv.Atoi(height)
	return w, h, errW == nil && errH == nil && w >= 1 && h >= 1 && w <= maxDimension && h <= maxDimension && strconv.Itoa(w) == width && strconv.Itoa(h) == height
}

// ResolveSize yields all linked common parameters at once, so a single
// resolution option cannot silently drop its resolution or aspect ratio when
// producing the upstream request or the price identity.
func (c SizeCapability) ResolveSize(in SizeInput) (ResolvedSize, error) {
	out := ResolvedSize{Values: map[ParameterKey]any{}}
	if err := c.Validate(); err != nil {
		return out, err
	}
	if in.Auto {
		if !c.Auto || in.Size != "" || in.Ratio != "" || in.Resolution != "" {
			return out, ErrInvalid
		}
		out.Values[Size] = "auto"
		out.Selection.Auto = true
		return out, nil
	}
	if in.Size == "auto" {
		return out, ErrInvalid
	}
	if c.Mode == WidthHeight {
		if in.Ratio != "" || in.Resolution != "" {
			return out, ErrInvalid
		}
		w, h, ok := parseSizePair(in.Size)
		if !ok || !c.containsDimensions(w, h) {
			return out, ErrInvalid
		}
		out.Values[Size] = in.Size
		out.Selection.Width, out.Selection.Height = w, h
		matched := len(c.Combinations) == 0
		for _, row := range c.Combinations {
			if row.Width == w && row.Height == h {
				out.Selection.Tier = row.Tier
				matched = true
				break
			}
		}
		if !matched {
			return ResolvedSize{Values: map[ParameterKey]any{}}, ErrInvalid
		}
		return out, nil
	}
	for _, row := range c.Combinations {
		match := row.Ratio == in.Ratio && (c.Mode == RatioSizeMap || row.Resolution == in.Resolution)
		if !match {
			continue
		}
		if row.Width > 0 {
			size := strconv.Itoa(row.Width) + "x" + strconv.Itoa(row.Height)
			if in.Size != "" && in.Size != size {
				return out, ErrInvalid
			}
			out.Values[Size] = size
			out.Selection.Width, out.Selection.Height = row.Width, row.Height
		} else if in.Size != "" {
			return out, ErrInvalid
		}
		out.Values[AspectRatio] = row.Ratio
		if c.Mode != RatioSizeMap && row.Resolution != "" {
			out.Values[Resolution] = row.Resolution
		}
		out.Selection.Tier = row.Tier
		return out, nil
	}
	return out, ErrInvalid
}
