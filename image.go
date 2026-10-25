package printkit

import (
	"context"
	"fmt"
	"strings"

	cdptype "github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	cdppage "github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

func (c *Converter) renderImage(ctx context.Context, req ImageRequest) ([]byte, error) {
	format, quality, err := imageEncoding(req)
	if err != nil {
		return nil, err
	}
	width := req.Width
	if width <= 0 {
		width = 1024
	}
	height := req.Height
	if height <= 0 {
		height = 800
	}
	scale := imageScale(req)
	full := req.FullPage || req.Height <= 0
	if req.Clip != nil {
		full = false
	}
	media := req.Media
	if media == "" {
		media = MediaScreen
	}

	var out []byte
	err = c.withTab(ctx, req.Network.Proxy, func(tab context.Context) error {
		tab, cancel := bound(tab, req.Wait.Timeout, req.Network.Timeout)
		defer cancel()
		info, cleanup, err := c.open(tab, navSpec{
			in:          req.Input,
			network:     req.Network,
			wait:        req.Wait,
			inject:      req.Inject,
			media:       media,
			grayscale:   req.Grayscale,
			viewportW:   width,
			viewportH:   height,
			deviceScale: scale,
			transparent: req.OmitBackground && format != cdppage.CaptureScreenshotFormatJpeg,
			load:        LoadErrorAbort,
		})
		if err != nil {
			return err
		}
		defer cleanup()
		_ = info
		if req.SmartWidth {
			scroll, err := documentScrollWidth(tab)
			if err != nil {
				return fmt.Errorf("printkit: measure layout: %w", err)
			}
			if int(scroll) > width {
				width = int(scroll)
				if width < 1 {
					width = 1
				}
				if _, err := chromedp.Call(tab, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{
					Width: int64(width), Height: int64(height), DeviceScaleFactor: scale,
				}); err != nil {
					return fmt.Errorf("printkit: viewport: %w", err)
				}
			}
		}
		params := cdppage.CaptureScreenshotParams{
			Format:      format,
			FromSurface: boolPtr(true),
		}
		if quality != nil && format != cdppage.CaptureScreenshotFormatPng {
			params.Quality = quality
		}
		switch {
		case req.Clip != nil:
			if req.Clip.Width <= 0 || req.Clip.Height <= 0 {
				return optionf("Clip", "width and height must be positive")
			}
			params.CaptureBeyondViewport = boolPtr(true)
			params.Clip = &cdppage.Viewport{
				X: req.Clip.X, Y: req.Clip.Y,
				Width: req.Clip.Width, Height: req.Clip.Height,
				Scale: 1,
			}
		case full:
			cw, ch, err := contentSize(tab, float64(width), float64(height))
			if err != nil {
				return err
			}
			if cw < 1 {
				cw = float64(width)
			}
			if ch < 1 {
				ch = float64(height)
			}
			params.CaptureBeyondViewport = boolPtr(true)
			params.Clip = &cdppage.Viewport{X: 0, Y: 0, Width: cw, Height: ch, Scale: 1}
		}
		res, err := chromedp.Call(tab, cdppage.CaptureScreenshot, params)
		if err != nil {
			return fmt.Errorf("printkit: screenshot: %w", err)
		}
		if len(res.Data) == 0 {
			return fmt.Errorf("printkit: chromium returned an empty image")
		}
		out = res.Data
		return nil
	})
	return out, err
}

func imageEncoding(req ImageRequest) (cdppage.CaptureScreenshotFormat, *int64, error) {
	name := strings.ToLower(strings.TrimSpace(string(req.Format)))
	switch name {
	case "", "png":
		return cdppage.CaptureScreenshotFormatPng, nil, nil
	case "jpeg", "jpg":
		q := req.Quality
		if q == 0 {
			q = 90
		}
		if q < 1 || q > 100 {
			return "", nil, optionf("Quality", "must be from 1 to 100")
		}
		v := int64(q)
		return cdppage.CaptureScreenshotFormatJpeg, &v, nil
	case "webp":
		q := req.Quality
		if q == 0 {
			q = 90
		}
		if q < 1 || q > 100 {
			return "", nil, optionf("Quality", "must be from 1 to 100")
		}
		v := int64(q)
		return cdppage.CaptureScreenshotFormatWebp, &v, nil
	default:
		return "", nil, optionf("Format", "must be png, jpeg, or webp")
	}
}

func imageScale(req ImageRequest) float64 {
	if req.Scale > 0 {
		return req.Scale
	}
	if req.DPI > 0 {
		return req.DPI / 96
	}
	return 1
}

func contentSize(ctx context.Context, fallbackW, fallbackH float64) (float64, float64, error) {
	metrics, err := chromedp.Call(ctx, cdppage.GetLayoutMetrics, cdptype.Empty{})
	if err != nil {
		return 0, 0, fmt.Errorf("printkit: layout metrics: %w", err)
	}
	if metrics.CSSContentSize == nil {
		return fallbackW, fallbackH, nil
	}
	w, h := metrics.CSSContentSize.Width, metrics.CSSContentSize.Height
	if w <= 0 {
		w = fallbackW
	}
	if h <= 0 {
		h = fallbackH
	}
	return w, h, nil
}
