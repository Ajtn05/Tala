package transcriber

/*
#include <string.h>
#include "whisper.h"

struct tala_vad_buffer {
	const unsigned char *next;
	size_t remaining;
};

static size_t tala_vad_read(void *context, void *output, size_t size) {
	struct tala_vad_buffer *buffer = context;
	size_t count = size < buffer->remaining ? size : buffer->remaining;
	memcpy(output, buffer->next, count);
	if (count < size) {
		memset((unsigned char *) output + count, 0, size - count);
	}
	buffer->next += count;
	buffer->remaining -= count;
	return count;
}

static bool tala_vad_eof(void *context) {
	return ((struct tala_vad_buffer *) context)->remaining == 0;
}

static void tala_vad_close(void *context) {
	(void) context;
}

static struct whisper_vad_context *tala_vad_open(const void *data, size_t size) {
	struct tala_vad_buffer buffer = {data, size};
	struct whisper_model_loader loader = {&buffer, tala_vad_read, tala_vad_eof, tala_vad_close};
	struct whisper_vad_context_params params = whisper_vad_default_context_params();
	params.use_gpu = false;
	params.n_threads = 1;
	return whisper_vad_init_with_params(&loader, params);
}
*/
import "C"

import (
	_ "embed"
	"time"
	"unsafe"
)

// Silero VAD v6.2 converted by ggml-org for whisper.cpp, under the MIT license.
//
//go:embed assets/ggml-silero-v6.2.0.bin
var speechModel []byte

type speechDetector struct {
	ctx *C.struct_whisper_vad_context
}

func newSpeechDetector() speechDetector {
	return speechDetector{ctx: C.tala_vad_open(unsafe.Pointer(&speechModel[0]), C.size_t(len(speechModel)))}
}

func (d speechDetector) close() {
	C.whisper_vad_free(d.ctx)
}

type speechSpan struct{ start, end time.Duration }

const samplePeriod = time.Second / SampleRate

// spans returns padded speech intervals on the original audio clock. A failed
// detector leaves the volume-based filter available.
func (d speechDetector) spans(samples []float32) ([]speechSpan, bool) {
	if d.ctx == nil {
		return nil, false
	}
	params := C.whisper_vad_default_params()
	params.threshold = 0.40
	params.min_speech_duration_ms = 96
	params.min_silence_duration_ms = 180
	params.speech_pad_ms = 180
	found := C.whisper_vad_segments_from_samples(d.ctx, params, (*C.float)(unsafe.Pointer(&samples[0])), C.int(len(samples)))
	if found == nil {
		return nil, false
	}
	defer C.whisper_vad_free_segments(found)
	end := time.Duration(len(samples)) * samplePeriod
	count := int(C.whisper_vad_segments_n_segments(found))
	spans := make([]speechSpan, 0, count)
	for i := 0; i < count; i++ {
		start := time.Duration(float64(C.whisper_vad_segments_get_segment_t0(found, C.int(i))) * float64(10*time.Millisecond))
		stop := time.Duration(float64(C.whisper_vad_segments_get_segment_t1(found, C.int(i))) * float64(10*time.Millisecond))
		if stop = min(stop, end); start < stop {
			spans = append(spans, speechSpan{start, stop})
		}
	}
	return spans, true
}

func speechOverlaps(spans []speechSpan, start, end time.Duration) bool {
	end = max(end, start+samplePeriod)
	for _, span := range spans {
		if start < span.end && end > span.start {
			return true
		}
	}
	return false
}

type speechChunk struct {
	samples []float32
	offset  time.Duration
}

// Keep short pauses as context and decode speech on each side of a long pause
// separately. Offsets keep the resulting timestamps on the original clock.
func speechChunks(samples []float32, spans []speechSpan) []speechChunk {
	const (
		edgeGap  = 500 * time.Millisecond
		splitGap = 3 * time.Second
		leadIn   = 320 * time.Millisecond
	)
	start := max(0, spans[0].start-leadIn)
	if spans[0].start < edgeGap {
		start = 0
	}
	duration := time.Duration(len(samples)) * samplePeriod
	chunks := make([]speechChunk, 0, 1)
	for i, span := range spans {
		end := span.end
		if i+1 < len(spans) {
			if spans[i+1].start-end < splitGap {
				continue
			}
		} else if duration-end < edgeGap {
			end = duration
		}
		first := int(start / samplePeriod)
		last := int((end + samplePeriod - 1) / samplePeriod)
		chunks = append(chunks, speechChunk{samples[first:last], time.Duration(first) * samplePeriod})
		if i+1 < len(spans) {
			start = max(0, spans[i+1].start-leadIn)
		}
	}
	return chunks
}
