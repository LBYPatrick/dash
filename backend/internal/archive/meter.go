package archive

import (
	"math"
	"time"
)

type meterSample struct {
	at    time.Time
	bytes int64
}
type transferMeter struct{ samples []meterSample }

func (m *Manager) measure(b *Batch, sent, issueBytes int64, now time.Time) {
	m.meterMu.Lock()
	defer m.meterMu.Unlock()
	if m.meters == nil {
		m.meters = map[string]*transferMeter{}
	}
	if b.Status != "running" || m.options.State() != "parked" || !b.ProgressReady {
		delete(m.meters, b.ID)
		return
	}
	meter := m.meters[b.ID]
	if meter == nil {
		meter = &transferMeter{}
		m.meters[b.ID] = meter
	}
	samples := meter.samples
	if len(samples) > 0 && sent < samples[len(samples)-1].bytes {
		samples = nil
	}
	if len(samples) == 0 || sent != samples[len(samples)-1].bytes || now.Sub(samples[len(samples)-1].at) >= time.Second {
		samples = append(samples, meterSample{now, sent})
	}
	for len(samples) > 2 && (now.Sub(samples[1].at) > 15*time.Second || len(samples) > 64) {
		samples = samples[1:]
	}
	meter.samples = samples
	if len(samples) < 2 {
		return
	}
	elapsed := now.Sub(samples[0].at).Seconds()
	delta := sent - samples[0].bytes
	if elapsed < 0.5 || delta <= 0 {
		return
	}
	// Measurements include source verification and time waiting for the shared
	// compute slot. Reused objects contribute zero bytes to the copy rate.
	b.BytesPerSecond = float64(delta) / elapsed
	remaining := max(int64(0), b.TotalBytes-b.TransferredBytes-issueBytes)
	if remaining > 0 && b.BytesPerSecond > 0 {
		seconds := int64(math.Ceil(float64(remaining) / b.BytesPerSecond))
		b.ETASeconds = &seconds
	}
}
