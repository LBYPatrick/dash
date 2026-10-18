package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"math"
	"os"
	"strconv"
)

const (
	thumbnailDarkPixel      = 18
	thumbnailClippedPixel   = 245
	thumbnailMinimumMedian  = 24
	thumbnailMinimumUpper   = 64
	thumbnailNearBestRatio  = 0.9
	thumbnailStableMedian   = 12
	thumbnailStableUpper    = 20
	thumbnailMaximumClipped = 0.25
)

type thumbnailExposure struct {
	median, upper, score float64
	clipped              float64
}

func (e thumbnailExposure) usable() bool {
	return (e.median >= thumbnailMinimumMedian || e.upper >= thumbnailMinimumUpper) && e.clipped < thumbnailMaximumClipped
}

// Measure the central scene rather than letterbox borders. Percentiles retain
// night detail without mistaking a small headlight or an AE flash for exposure.
func measureThumbnail(frame image.Image) thumbnailExposure {
	bounds := frame.Bounds()
	var histogram [256]int
	count := 0
	for y := 0; y < 24; y++ {
		for x := 0; x < 32; x++ {
			px := bounds.Min.X + bounds.Dx()*(10*32+80*x)/(100*32)
			py := bounds.Min.Y + bounds.Dy()*(15*24+70*y)/(100*24)
			value := color.GrayModel.Convert(frame.At(px, py)).(color.Gray).Y
			histogram[value]++
			count++
		}
	}
	percentile := func(percent int) float64 {
		total := 0
		for value, frequency := range histogram {
			total += frequency
			if total*100 >= count*percent {
				return float64(value)
			}
		}
		return 0
	}
	black, clipped := 0, 0
	for value, frequency := range histogram {
		if value < thumbnailDarkPixel {
			black += frequency
		}
		if value >= thumbnailClippedPixel {
			clipped += frequency
		}
	}
	median, upper, lower := percentile(50), percentile(90), percentile(10)
	clipping := float64(clipped) / float64(count)
	score := (0.5*median + 0.25*upper + 0.25*(upper-lower)) * math.Pow(1-clipping, 2)
	score -= 20 * float64(black) / float64(count)
	return thumbnailExposure{median: median, upper: upper, clipped: clipping, score: score}
}

// Prefer the earliest stable pair near the best exposure. If the whole clip is
// dark or too short, keep its most useful real frame without brightening it.
func selectThumbnail(exposures []thumbnailExposure) int {
	best := 0
	for n := range exposures {
		currentClipped := exposures[n].clipped >= thumbnailMaximumClipped
		bestClipped := exposures[best].clipped >= thumbnailMaximumClipped
		if (!currentClipped && bestClipped) || (currentClipped == bestClipped && exposures[n].score > exposures[best].score) {
			best = n
		}
	}
	for n := 0; n+1 < len(exposures); n++ {
		current, next := exposures[n], exposures[n+1]
		if current.usable() && next.usable() && current.score >= exposures[best].score*thumbnailNearBestRatio &&
			math.Abs(current.median-next.median) <= thumbnailStableMedian && math.Abs(current.upper-next.upper) <= thumbnailStableUpper {
			return n
		}
	}
	return best
}

type thumbnailBuffer struct{ value bytes.Buffer }

func (b *thumbnailBuffer) Bytes() []byte { return b.value.Bytes() }
func (b *thumbnailBuffer) Len() int      { return b.value.Len() }

func (b *thumbnailBuffer) Write(data []byte) (int, error) {
	if b.Len()+len(data) > ThumbnailOutputLimit {
		return 0, errors.New("thumbnail sample output exceeds budget")
	}
	return b.value.Write(data)
}

// JPEG decoders can read ahead into the next image. Delimit each encoded frame
// first, honoring segment lengths, byte stuffing and restart markers.
func thumbnailJPEGSize(data []byte) (int, error) {
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return 0, errors.New("invalid thumbnail JPEG start")
	}
	entropy := false
	for position := 2; position < len(data); {
		if entropy {
			next := bytes.IndexByte(data[position:], 0xff)
			if next < 0 {
				break
			}
			position += next
		} else if data[position] != 0xff {
			return 0, errors.New("invalid thumbnail JPEG marker")
		}
		for position < len(data) && data[position] == 0xff {
			position++
		}
		if position == len(data) {
			break
		}
		marker := data[position]
		position++
		if entropy && (marker == 0 || marker >= 0xd0 && marker <= 0xd7) {
			continue
		}
		if marker == 0xd9 {
			return position, nil
		}
		if marker == 0xd8 || marker == 0 || position+2 > len(data) {
			break
		}
		length := int(data[position])<<8 | int(data[position+1])
		if length < 2 || length > len(data)-position {
			break
		}
		position += length
		entropy = marker == 0xda
	}
	return 0, errors.New("incomplete thumbnail JPEG")
}

func sampleThumbnail(ctx context.Context, camera string) (image.Image, error) {
	var output thumbnailBuffer
	filter := "fps=" + strconv.Itoa(ThumbnailFPS) + ":round=up:eof_action=pass,scale=" + strconv.Itoa(ThumbnailWidth) + ":" + strconv.Itoa(ThumbnailHeight) + ":force_original_aspect_ratio=decrease:force_divisible_by=2"
	err := runFFmpegTo(ctx, &output, "-threads", strconv.Itoa(IndexMediaThreads), "-filter_threads", "1", "-probesize", "1048576", "-analyzeduration", "1000000", "-i", camera,
		"-map", "0:v:0", "-an", "-sn", "-dn", "-t", strconv.FormatFloat(float64(ThumbnailSamples)/ThumbnailFPS, 'f', 2, 64), "-frames:v", strconv.Itoa(ThumbnailSamples), "-vf", filter,
		"-c:v", "mjpeg", "-q:v", "5", "-threads", "1", "-f", "image2pipe", "pipe:1")
	if err != nil {
		return nil, err
	}
	data := output.Bytes()
	frames := []image.Image{}
	exposures := []thumbnailExposure{}
	for len(data) > 0 && len(frames) < ThumbnailSamples {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		length, err := thumbnailJPEGSize(data)
		if err != nil {
			return nil, err
		}
		frame, err := jpeg.Decode(bytes.NewReader(data[:length]))
		if err != nil {
			return nil, err
		}
		frames = append(frames, frame)
		exposures = append(exposures, measureThumbnail(frame))
		data = data[length:]
	}
	if len(frames) == 0 {
		return nil, errors.New("recording has no thumbnail frames")
	}
	return frames[selectThumbnail(exposures)], nil
}

func existingThumbnailUsable(filename string) bool {
	file, err := os.Open(filename)
	if err != nil {
		return false
	}
	defer file.Close()
	// Inspect only bounded app-generated JPEGs, never original camera files.
	config, err := jpeg.DecodeConfig(io.LimitReader(file, ThumbnailOutputLimit))
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > 1024 || config.Height > 1024 {
		return false
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return false
	}
	frame, err := jpeg.Decode(io.LimitReader(file, ThumbnailOutputLimit))
	return err == nil && measureThumbnail(frame).usable()
}
