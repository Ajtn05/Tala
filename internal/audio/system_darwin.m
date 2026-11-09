#import <CoreAudio/AudioHardwareTapping.h>
#import <CoreAudio/CATapDescription.h>
#import <CoreAudio/CoreAudio.h>
#import <Foundation/Foundation.h>
#include <mach/mach_time.h>
#include <stdatomic.h>
#include "system_darwin.h"

enum { RingSize = 1 << 17 };

struct TalaSystemInput {
    AudioObjectID tap;
    AudioObjectID device;
    AudioDeviceIOProcID procedure;
    int rate;
    bool running;
    _Atomic bool overflow;
    _Atomic uint64_t firstTime;
    _Atomic uint64_t produced;
    _Atomic uint64_t consumed;
    float ring[RingSize];
};

static OSStatus receiveAudio(AudioObjectID device, const AudioTimeStamp *now,
    const AudioBufferList *data, const AudioTimeStamp *inputTime,
    AudioBufferList *output, const AudioTimeStamp *outputTime, void *user) {
    TalaSystemInput *input = user;
    if (!data || data->mNumberBuffers == 0
        || atomic_load_explicit(&input->overflow, memory_order_relaxed)) return noErr;

    // The tap is a mono mixdown and its Float32 format is validated at open.
    const AudioBuffer *buffer = &data->mBuffers[0];
    if (!buffer->mData || !buffer->mDataByteSize) return noErr;
    unsigned count = buffer->mDataByteSize / sizeof(float);
    uint64_t end = atomic_load_explicit(&input->produced, memory_order_relaxed);
    uint64_t start = atomic_load_explicit(&input->consumed, memory_order_acquire);
    if (end - start + count > RingSize) {
        atomic_store_explicit(&input->overflow, true, memory_order_release);
        return noErr;
    }
    if (end == 0) {
        uint64_t host = inputTime && (inputTime->mFlags & kAudioTimeStampHostTimeValid)
            ? inputTime->mHostTime : mach_absolute_time();
        atomic_store_explicit(&input->firstTime, AudioConvertHostTimeToNanos(host), memory_order_relaxed);
    }
    const float *samples = buffer->mData;
    for (unsigned i = 0; i < count; i++) {
        input->ring[(end + i) & (RingSize - 1)] = samples[i];
    }
    atomic_store_explicit(&input->produced, end + count, memory_order_release);
    return noErr;
}

TalaSystemInput *tala_system_open(int32_t *status) {
    @autoreleasepool {
        TalaSystemInput *input = calloc(1, sizeof(*input));
        if (!input) { *status = kAudioHardwareUnspecifiedError; return NULL; }
        atomic_init(&input->overflow, false);
        atomic_init(&input->firstTime, 0);
        atomic_init(&input->produced, 0);
        atomic_init(&input->consumed, 0);
        CATapDescription *tap = [[CATapDescription alloc] initMonoGlobalTapButExcludeProcesses:@[]];
        tap.name = @"Tala System Audio";
        tap.privateTap = YES;
        *status = AudioHardwareCreateProcessTap(tap, &input->tap);
        if (*status != noErr) goto failed;

        AudioObjectPropertyAddress address = {
            kAudioTapPropertyFormat, kAudioObjectPropertyScopeGlobal, kAudioObjectPropertyElementMain
        };
        AudioStreamBasicDescription format;
        UInt32 size = sizeof(format);
        *status = AudioObjectGetPropertyData(input->tap, &address, 0, NULL, &size, &format);
        if (*status != noErr) goto failed;
        if (format.mFormatID != kAudioFormatLinearPCM || !(format.mFormatFlags & kAudioFormatFlagIsFloat)
            || format.mBitsPerChannel != 32 || format.mChannelsPerFrame != 1
            || !(format.mSampleRate >= 8000 && format.mSampleRate <= 384000)) {
            *status = kAudioHardwareUnsupportedOperationError;
            goto failed;
        }
        input->rate = (int)(format.mSampleRate + 0.5);

        NSDictionary *specification = @{
            @kAudioAggregateDeviceNameKey: @"Tala Audio Input",
            @kAudioAggregateDeviceUIDKey: NSUUID.UUID.UUIDString,
            @kAudioAggregateDeviceIsPrivateKey: @YES,
            @kAudioAggregateDeviceTapListKey: @[@{
                @kAudioSubTapUIDKey: tap.UUID.UUIDString,
                @kAudioSubTapDriftCompensationKey: @YES
            }]
        };
        *status = AudioHardwareCreateAggregateDevice((__bridge CFDictionaryRef)specification, &input->device);
        if (*status != noErr) goto failed;
        *status = AudioDeviceCreateIOProcID(input->device, receiveAudio, input, &input->procedure);
        if (*status != noErr) goto failed;
        *status = AudioDeviceStart(input->device, input->procedure);
        if (*status != noErr) goto failed;
        input->running = true;
        [tap release];
        return input;

    failed:
        tala_system_close(input);
        [tap release];
        return NULL;
    }
}

int tala_system_rate(TalaSystemInput *input) { return input->rate; }
uint64_t tala_system_time(void) { return AudioConvertHostTimeToNanos(mach_absolute_time()); }
uint64_t tala_system_first_time(TalaSystemInput *input) {
    return atomic_load_explicit(&input->firstTime, memory_order_relaxed);
}

int tala_system_read(TalaSystemInput *input, float *samples, int capacity) {
    uint64_t start = atomic_load_explicit(&input->consumed, memory_order_relaxed);
    uint64_t available = atomic_load_explicit(&input->produced, memory_order_acquire) - start;
    int count = available < (uint64_t)capacity ? (int)available : capacity;
    if (count == 0 && atomic_load_explicit(&input->overflow, memory_order_acquire)) return -1;
    for (int i = 0; i < count; i++) {
        samples[i] = input->ring[(start + i) & (RingSize - 1)];
    }
    atomic_store_explicit(&input->consumed, start + count, memory_order_release);
    return count;
}

void tala_system_stop(TalaSystemInput *input) {
    if (input->running) {
        AudioDeviceStop(input->device, input->procedure);
        input->running = false;
    }
}

void tala_system_close(TalaSystemInput *input) {
    tala_system_stop(input);
    if (input->procedure) AudioDeviceDestroyIOProcID(input->device, input->procedure);
    if (input->device) AudioHardwareDestroyAggregateDevice(input->device);
    if (input->tap) AudioHardwareDestroyProcessTap(input->tap);
    free(input);
}
