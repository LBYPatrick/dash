package telemetry

// CaptureSpan prefers loggerd's segment sentinels over queued event receive
// order. Repeated route initData and stale queued events cannot stretch a part.
type CaptureSpan struct {
	first, last, start, end uint64
	seen, hasStart, hasEnd  bool
}

func (s *CaptureSpan) Observe(record Record) {
	if !record.Valid || record.Kind == 0 {
		return
	}
	if !s.seen || record.MonotonicNS < s.first {
		s.first = record.MonotonicNS
	}
	if !s.seen || record.MonotonicNS > s.last {
		s.last = record.MonotonicNS
	}
	s.seen = true
	if record.Boundary == nil {
		return
	}
	switch *record.Boundary {
	case 2, 3:
		if !s.hasStart || record.MonotonicNS < s.start {
			s.start = record.MonotonicNS
		}
		s.hasStart = true
	case 0, 1:
		if !s.hasEnd || record.MonotonicNS > s.end {
			s.end = record.MonotonicNS
		}
		s.hasEnd = true
	}
}
func (s CaptureSpan) Bounds() (start, end uint64, observed bool) {
	start, end = s.first, s.last
	if s.hasStart {
		start = s.start
	}
	if s.hasEnd {
		end = s.end
	}
	if end < start {
		end = start
	}
	return start, end, s.seen
}
