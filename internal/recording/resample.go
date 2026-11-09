package recording

import "tala/internal/transcriber"

// resample averages each output interval over the source samples. Integer
// positions keep the last interval in bounds even at fractional rate ratios.
func resample(input []float32, rate int) []float32 {
	const target = transcriber.SampleRate
	if rate == target {
		return input
	}
	step := int64(rate)
	limit := int64(len(input)) * target
	output := make([]float32, (limit+step-1)/step)
	for i := range output {
		start := int64(i) * step
		end := min(start+step, limit)
		sum := 0.0
		for j := start / target; j*target < end; j++ {
			width := min(end, (j+1)*target) - max(start, j*target)
			sum += float64(width) * float64(input[j])
		}
		output[i] = float32(sum / float64(end-start))
	}
	return output
}
