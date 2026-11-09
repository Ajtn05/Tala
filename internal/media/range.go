package media

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// TimeRange selects part of the original media timeline, in seconds.
// End == 0 means through the end of the media. The zero value selects all audio.
type TimeRange struct {
	Start float64 `json:"start,omitempty"`
	End   float64 `json:"end,omitempty"`
}

const maxTimestamp = float64((1<<63 - 1) / 1e9)

var timestampPattern = regexp.MustCompile(`^\d+(?::\d{1,2}){0,2}(?:\.\d{1,3})?$`)

// ParseTimestamp accepts seconds, MM:SS, or HH:MM:SS, with optional milliseconds.
func ParseTimestamp(raw string) (float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	if !timestampPattern.MatchString(raw) {
		return 0, errors.New("use seconds, MM:SS, or HH:MM:SS (optional milliseconds)")
	}
	parts := strings.Split(raw, ":")
	total := 0.0
	for i, part := range parts {
		n, err := strconv.ParseFloat(part, 64)
		if err != nil || (i > 0 && n >= 60) {
			return 0, errors.New("minutes and seconds after a colon must be below 60")
		}
		total = total*60 + n
	}
	if total > maxTimestamp {
		return 0, errors.New("timestamp is too large")
	}
	return total, nil
}

func ParseRange(start, end string) (TimeRange, error) {
	a, err := ParseTimestamp(start)
	if err != nil {
		return TimeRange{}, fmt.Errorf("start time: %w", err)
	}
	b, err := ParseTimestamp(end)
	if err != nil {
		return TimeRange{}, fmt.Errorf("end time: %w", err)
	}
	if strings.TrimSpace(end) != "" && b == 0 {
		return TimeRange{}, errors.New("end time must be after start time; leave it blank for the full remaining audio")
	}
	r := TimeRange{Start: a, End: b}
	return r, r.Validate(0)
}

// Validate checks duration bounds when a positive duration is known.
func (r TimeRange) Validate(duration float64) error {
	for _, n := range []float64{r.Start, r.End} {
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > maxTimestamp {
			return errors.New("timestamps must be finite, nonnegative times")
		}
	}
	if r.End != 0 && r.End <= r.Start {
		return errors.New("end time must be after start time")
	}
	if duration > 0 {
		if r.Start >= duration {
			return errors.New("start time must be before the end of the media")
		}
		if r.End > duration {
			return errors.New("end time must not exceed the media duration")
		}
	}
	return nil
}

func (r TimeRange) Duration(sourceDuration float64) float64 {
	end := r.End
	if end == 0 {
		end = sourceDuration
	}
	return math.Max(0, end-r.Start)
}
