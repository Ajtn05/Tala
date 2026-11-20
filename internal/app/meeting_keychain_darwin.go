package app

/*
#cgo LDFLAGS: -framework CoreFoundation -framework Security
#include <Security/Security.h>
#include <stdlib.h>

static CFMutableDictionaryRef meetingKeyQuery(CFStringRef account) {
	CFMutableDictionaryRef query = CFDictionaryCreateMutable(kCFAllocatorDefault, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFDictionarySetValue(query, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(query, kSecAttrService, CFSTR("io.github.ajtn05.tala.next.meeting-provider"));
	CFDictionarySetValue(query, kSecAttrAccount, account);
	return query;
}

enum { meetingKeyExists, meetingKeyRead, meetingKeyWrite, meetingKeyDelete };

static OSStatus meetingKey(int operation, const char *provider, const UInt8 *key, CFIndex length, CFDataRef *result) {
	CFStringRef account = CFStringCreateWithCString(kCFAllocatorDefault, provider, kCFStringEncodingUTF8);
	CFMutableDictionaryRef query = meetingKeyQuery(account);
	CFRelease(account);
	OSStatus status;
	switch (operation) {
	case meetingKeyWrite: {
		CFDataRef data = CFDataCreate(kCFAllocatorDefault, key, length);
		const void *attr = kSecValueData, *value = data;
		CFDictionaryRef update = CFDictionaryCreate(kCFAllocatorDefault, &attr, &value, 1,
			&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
		CFDictionarySetValue(query, kSecValueData, data);
		status = SecItemAdd(query, NULL);
		if (status == errSecDuplicateItem) {
			CFDictionaryRemoveValue(query, kSecValueData);
			status = SecItemUpdate(query, update);
		}
		CFRelease(update);
		CFRelease(data);
		break;
	}
	case meetingKeyDelete:
		status = SecItemDelete(query);
		break;
	case meetingKeyRead:
		CFDictionarySetValue(query, kSecReturnData, kCFBooleanTrue);
		status = SecItemCopyMatching(query, (CFTypeRef *)result);
		break;
	default:
		status = SecItemCopyMatching(query, NULL);
	}
	CFRelease(query);
	return status;
}

static void releaseMeetingKey(CFDataRef result) { CFRelease(result); }
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

type macMeetingKeys struct{}

func newMeetingKeyStore() meetingKeyStore { return macMeetingKeys{} }

func (macMeetingKeys) Has(provider string) bool {
	return meetingKeyOperation(C.meetingKeyExists, provider, "", nil) == C.errSecSuccess
}

func (macMeetingKeys) Load(provider string) (string, error) {
	var data C.CFDataRef
	status := meetingKeyOperation(C.meetingKeyRead, provider, "", &data)
	if status == C.errSecItemNotFound {
		return "", errors.New("save the provider’s API key in Meeting notes settings")
	}
	if status != C.errSecSuccess {
		return "", fmt.Errorf("could not read the API key from macOS Keychain (%d)", status)
	}
	defer C.releaseMeetingKey(data)
	return C.GoStringN((*C.char)(unsafe.Pointer(C.CFDataGetBytePtr(data))), C.int(C.CFDataGetLength(data))), nil
}

func (macMeetingKeys) Save(provider, key string) error {
	if status := meetingKeyOperation(C.meetingKeyWrite, provider, key, nil); status != C.errSecSuccess {
		return fmt.Errorf("could not save the API key in macOS Keychain (%d)", status)
	}
	return nil
}

func (macMeetingKeys) Remove(provider string) error {
	if status := meetingKeyOperation(C.meetingKeyDelete, provider, "", nil); status != C.errSecSuccess && status != C.errSecItemNotFound {
		return fmt.Errorf("could not remove the API key from macOS Keychain (%d)", status)
	}
	return nil
}

func meetingKeyOperation(op C.int, provider, key string, result *C.CFDataRef) C.OSStatus {
	name := C.CString(provider)
	defer C.free(unsafe.Pointer(name))
	return C.meetingKey(op, name, (*C.UInt8)(unsafe.Pointer(unsafe.StringData(key))), C.CFIndex(len(key)), result)
}
