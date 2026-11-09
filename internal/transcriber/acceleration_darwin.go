package transcriber

/*
#cgo CFLAGS: -I${SRCDIR}/../../third_party/whisper.cpp/ggml/src/ggml-metal
#include "ggml-metal-device.h"
*/
import "C"

// TensorAccelerationActive reports the Metal mode after Whisper has loaded a model.
func TensorAccelerationActive() bool {
	device := C.ggml_metal_device_get()
	return bool(C.ggml_metal_device_get_props(device).has_tensor) &&
		C.ggml_metal_device_get_queue(device) != nil &&
		C.ggml_metal_device_get_library(device) != nil
}
