package printkit

const (
	defaultMarginMM = 10.0
	headerMarginMM  = 16.0
)

type pageBox struct {
	width, height                                    float64
	marginTop, marginRight, marginBottom, marginLeft float64
}

func (p Page) box(hasHeader, hasFooter bool) (pageBox, error) {
	var w, h Length
	var err error
	if !p.Width.IsZero() || !p.Height.IsZero() {
		if p.Width.IsZero() || p.Height.IsZero() {
			return pageBox{}, optionf("Page", "width and height must both be set")
		}
		if p.Width.Value < 0 || p.Height.Value < 0 {
			return pageBox{}, optionf("Page", "width and height must be positive")
		}
		w, h = p.Width, p.Height
	} else {
		w, h, err = PaperSize(p.Size)
		if err != nil {
			return pageBox{}, err
		}
	}
	orient := p.Orientation
	if orient == "" {
		orient = Portrait
	}
	switch orient {
	case Portrait, "portrait":
	case Landscape, "landscape":
		w, h = h, w
	default:
		return pageBox{}, optionf("Page.Orientation", "must be Portrait or Landscape")
	}
	def := MM(defaultMarginMM).Inches()
	box := pageBox{width: w.Inches(), height: h.Inches()}
	if p.Margin.custom {
		box.marginTop = p.Margin.Top.Inches()
		box.marginRight = p.Margin.Right.Inches()
		box.marginBottom = p.Margin.Bottom.Inches()
		box.marginLeft = p.Margin.Left.Inches()
	} else {
		box.marginTop = def
		box.marginRight = def
		box.marginBottom = def
		box.marginLeft = def
		floor := MM(headerMarginMM).Inches()
		if hasHeader && box.marginTop < floor {
			box.marginTop = floor
		}
		if hasFooter && box.marginBottom < floor {
			box.marginBottom = floor
		}
	}
	for _, m := range []float64{box.marginTop, box.marginRight, box.marginBottom, box.marginLeft} {
		if m < 0 {
			return pageBox{}, optionf("Page.Margin", "margins must not be negative")
		}
	}
	if box.width <= box.marginLeft+box.marginRight || box.height <= box.marginTop+box.marginBottom {
		return pageBox{}, optionf("Page.Margin", "margins are larger than the page")
	}
	return box, nil
}

func (b pageBox) contentWidthPx() float64 {
	return (b.width - b.marginLeft - b.marginRight) * 96
}

func (b pageBox) contentHeightPx() float64 {
	return (b.height - b.marginTop - b.marginBottom) * 96
}

func clampScale(scale float64) float64 {
	if scale == 0 {
		scale = 1
	}
	if scale < 0.1 {
		return 0.1
	}
	if scale > 2 {
		return 2
	}
	return scale
}

// fitScale applies the smart-shrinking approximation: a layout wider than the
// printable width is scaled down so it fits. userScale 0 means 1.
func fitScale(userScale, scrollWidthPx, contentWidthPx float64, shrink bool) float64 {
	if userScale == 0 {
		userScale = 1
	}
	if shrink && scrollWidthPx > contentWidthPx+1 && contentWidthPx > 0 {
		userScale *= contentWidthPx / scrollWidthPx
	}
	return clampScale(userScale)
}
