package media

import (
	"context"
	"math"
	"testing"
)

func TestParseRange(t *testing.T) {
	for _, tc := range []struct {
		start, end string
		want       TimeRange
	}{
		{"", "", TimeRange{}},
		{" 90.125 ", "2:05.250", TimeRange{90.125, 125.25}},
		{"01:02:03", "", TimeRange{3723, 0}},
		{"", "01:00", TimeRange{0, 60}},
	} {
		got, err := ParseRange(tc.start, tc.end)
		if err != nil || got != tc.want {
			t.Errorf("ParseRange(%q, %q) = %+v, %v", tc.start, tc.end, got, err)
		}
	}
	for _, tc := range [][2]string{
		{"-1", ""}, {"1:60", ""}, {"1:60:00", ""}, {"NaN", ""},
		{"Inf", ""}, {"1e2", ""}, {"1:2:3:4", ""}, {"1.1234", ""},
		{"1:02.5:03", ""}, {"9223372037", ""}, {"3", "3"}, {"5", "4"}, {"", "0"},
	} {
		if _, err := ParseRange(tc[0], tc[1]); err == nil {
			t.Errorf("accepted %v", tc)
		}
	}
}

func TestRangeValidation(t *testing.T) {
	for _, r := range []TimeRange{{-1, 0}, {0, -1}, {math.NaN(), 0}, {0, math.Inf(1)}, {10, 5}, {10, 10}, {60, 0}, {0, 61}} {
		if err := r.Validate(60); err == nil {
			t.Errorf("accepted invalid range %+v", r)
		}
	}
	for _, r := range []TimeRange{{}, {1, 0}, {0, 60}, {1, 2}} {
		if err := r.Validate(60); err != nil {
			t.Errorf("rejected %+v: %v", r, err)
		}
	}
	if got := (TimeRange{Start: 10, End: 30}).Duration(60); got != 20 {
		t.Fatalf("selected duration = %v", got)
	}
}

func TestStreamRangePreservesOriginalTimelineAndSamples(t *testing.T) {
	p := synthetic(t, 63)
	var full []float32
	if err := Stream(context.Background(), p, func(s []float32, _ float64) error {
		full = append(full, s...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, selection := range []TimeRange{{Start: 10.125, End: 42.625}, {Start: 60.25}, {End: 1.5}} {
		var got []float32
		err := StreamRange(context.Background(), p, selection, func(s []float32, offset float64) error {
			wantOffset := selection.Start + float64(len(got))/16000
			if offset != wantOffset {
				t.Errorf("offset = %v, want %v", offset, wantOffset)
			}
			got = append(got, s...)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		end := selection.End
		if end == 0 {
			end = 63
		}
		want := full[int(selection.Start*16000):int(end*16000)]
		if len(got) != len(want) {
			t.Fatalf("range %+v: %d samples, want %d", selection, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("range %+v: sample %d differs", selection, i)
			}
		}
	}
}

func TestStreamRangeBeyondUnknownDurationReturnsNoAudio(t *testing.T) {
	p := synthetic(t, 1)
	err := StreamRange(context.Background(), p, TimeRange{Start: 5}, func([]float32, float64) error {
		t.Fatal("consumed audio beyond EOF")
		return nil
	})
	if err == nil {
		t.Fatal("empty range succeeded")
	}
}
