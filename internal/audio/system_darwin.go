package audio

/*
#cgo darwin LDFLAGS: -framework CoreAudio -framework Foundation
#include "system_darwin.h"
*/
import "C"

import (
	"context"
	"fmt"
	"time"
	"unsafe"
)

func systemAudio(ctx context.Context, origin time.Time, emit func(Frame) error) error {
	if ctx.Err() != nil {
		return nil
	}
	originHost := int64(C.tala_system_time()) - time.Since(origin).Nanoseconds()
	var status C.int32_t
	input := C.tala_system_open(&status)
	if input == nil {
		return fmt.Errorf("open system audio (Core Audio status %d); check system audio recording permission", int32(status))
	}
	defer C.tala_system_close(input)

	rate := int(C.tala_system_rate(input))
	scratch := make([]float32, rate/50)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	var position int64
	var start time.Duration
	stopped := false
	for {
		n := int(C.tala_system_read(input, (*C.float)(unsafe.Pointer(&scratch[0])), C.int(len(scratch))))
		switch {
		case n < 0:
			return fmt.Errorf("system audio input overflowed")
		case n > 0:
			if position == 0 {
				start = max(0, time.Duration(int64(C.tala_system_first_time(input))-originHost))
			}
			frame := Frame{Source: System, Rate: rate, Start: start + Elapsed(position, rate)}
			frame.Samples = append([]float32(nil), scratch[:n]...)
			if err := emit(frame); err != nil {
				return err
			}
			position += int64(n)
		case stopped:
			return nil
		default:
			select {
			case <-ctx.Done():
				C.tala_system_stop(input)
				stopped = true
			case <-tick.C:
			}
		}
		if !stopped && ctx.Err() != nil {
			C.tala_system_stop(input)
			stopped = true
		}
	}
}
