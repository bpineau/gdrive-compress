package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	"math"
	"os"
	"os/exec"
)

const (
	minQuality = 40
	maxQuality = 92
)

// compressJPEG re-encodes a JPEG to the largest size <= maxSize bytes,
// preserving EXIF. It binary-searches the JPEG quality factor first;
// if even q=40 still exceeds maxSize, it progressively downscales the
// longest edge and retries.
//
// Strategy:
//   - Always start from the original buffer (compounding lossy passes
//     would degrade quality unnecessarily).
//   - Binary-search q in [40, 92]. Accept the highest q whose size <= maxSize.
//   - If best q=40 still > maxSize, downscale longest-edge to {2400, 1920,
//     1600, 1280, 1024} and retry the binary search at each step.
func compressJPEG(ctx context.Context, in []byte, maxSize int) ([]byte, string, error) {
	tmpIn, err := writeTemp(in)
	if err != nil {
		return nil, "", err
	}
	defer os.Remove(tmpIn)

	// First try at native resolution.
	best, q, fits, err := searchQuality(ctx, tmpIn, "", maxSize)
	if err != nil {
		return nil, "", err
	}
	if fits {
		return best, fmt.Sprintf("native q=%d", q), nil
	}

	// Need to downscale. Rungs >= the original longest edge are no-ops
	// ("only shrink") that would repeat the native search — skip them.
	longestEdge := math.MaxInt
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(in)); err == nil {
		longestEdge = max(cfg.Width, cfg.Height)
	}
	for _, edge := range []int{2400, 1920, 1600, 1280, 1024} {
		if edge >= longestEdge {
			continue
		}
		resize := fmt.Sprintf("%dx%d>", edge, edge) // ImageMagick: only shrink
		// Size is monotone in quality: if even minQuality exceeds maxSize,
		// the whole rung is hopeless — one probe instead of a full search.
		floor, err := encodeJPEG(ctx, tmpIn, resize, minQuality)
		if err != nil {
			return nil, "", err
		}
		if len(floor) > maxSize {
			best = floor
			continue
		}
		buf, q, fits, err := searchQuality(ctx, tmpIn, resize, maxSize)
		if err != nil {
			return nil, "", err
		}
		if fits {
			return buf, fmt.Sprintf("resize=%d q=%d", edge, q), nil
		}
		best = buf
	}
	return best, "best-effort", nil
}

// searchQuality binary-searches the JPEG quality in [minQuality, maxQuality].
// It returns the highest-quality buffer that is <= maxSize (fits=true), or —
// if none fits — the smallest buffer produced (fits=false, best effort for
// the caller to keep iterating).
func searchQuality(ctx context.Context, srcPath, resize string, maxSize int) (buf []byte, q int, fits bool, err error) {
	lo, hi := minQuality, maxQuality
	var bestFit, smallest []byte
	var bestFitQ, smallestQ int
	for lo <= hi {
		mid := (lo + hi) / 2
		out, err := encodeJPEG(ctx, srcPath, resize, mid)
		if err != nil {
			return nil, 0, false, err
		}
		if smallest == nil || len(out) < len(smallest) {
			smallest, smallestQ = out, mid
		}
		if len(out) > maxSize {
			hi = mid - 1
			continue
		}
		// fits — try to push quality higher.
		bestFit, bestFitQ = out, mid
		lo = mid + 1
	}
	if bestFit != nil {
		return bestFit, bestFitQ, true, nil
	}
	return smallest, smallestQ, false, nil
}

func encodeJPEG(ctx context.Context, srcPath, resize string, quality int) ([]byte, error) {
	args := []string{srcPath}
	if resize != "" {
		args = append(args, "-resize", resize)
	}
	args = append(args,
		"-sampling-factor", "4:2:0",
		"-quality", fmt.Sprintf("%d", quality),
		"-interlace", "Plane",
		"-define", "jpeg:optimize-coding=true",
		"jpeg:-",
	)
	cmd := exec.CommandContext(ctx, "magick", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("magick: %w (%s)", err, stderr.String())
	}
	return stdout.Bytes(), nil
}

func writeTemp(data []byte) (string, error) {
	f, err := os.CreateTemp("", "gdc-*.jpg")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
