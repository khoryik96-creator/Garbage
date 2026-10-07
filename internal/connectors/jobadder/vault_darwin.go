package jobadder

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <stdlib.h>
#include <string.h>

static OSStatus vault(int action, const char *account, const void *input, int size, void **output, int *length) {
 CFStringRef a = CFStringCreateWithCString(NULL, account, kCFStringEncodingUTF8);
 const void *keys[] = { kSecClass, kSecAttrService, kSecAttrAccount };
 const void *values[] = { kSecClassGenericPassword, CFSTR("com.garbagetruck.jobadder"), a };
 CFMutableDictionaryRef q = CFDictionaryCreateMutable(NULL, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
 for (int i = 0; i < 3; i++) CFDictionarySetValue(q, keys[i], values[i]);
 CFDictionarySetValue(q, kSecUseAuthenticationUI, kSecUseAuthenticationUIFail);
 OSStatus status;
 if (action == 0) {
  CFDictionarySetValue(q, kSecReturnData, kCFBooleanTrue);
  CFDataRef data = NULL;
  status = SecItemCopyMatching(q, (CFTypeRef *)&data);
  if (status == errSecSuccess) {
   *length = (int)CFDataGetLength(data); *output = malloc(*length);
   if (*output == NULL) status = errSecAllocate;
   else memcpy(*output, CFDataGetBytePtr(data), *length);
   CFRelease(data);
  }
 } else if (action == 1) {
  CFDataRef data = CFDataCreate(NULL, input, size);
  const void *key = kSecValueData, *value = data;
  CFDictionaryRef update = CFDictionaryCreate(NULL, &key, &value, 1, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
  status = SecItemUpdate(q, update);
  if (status == errSecItemNotFound) { CFDictionarySetValue(q, key, value); status = SecItemAdd(q, NULL); }
  CFRelease(update); CFRelease(data);
 } else { status = SecItemDelete(q); if (status == errSecItemNotFound) status = errSecSuccess; }
 CFRelease(q); CFRelease(a); return status;
}
*/
import "C"

import (
	"crypto/sha256"
	"fmt"
	"unsafe"
)

type keychainVault struct{ account string }

func NewVault(directory string) Vault {
	return keychainVault{fmt.Sprintf("%x", sha256.Sum256([]byte(directory)))}
}
func (v keychainVault) Available() bool { return true }
func (v keychainVault) operate(action int, data []byte) ([]byte, error) {
	account := C.CString(v.account)
	defer C.free(unsafe.Pointer(account))
	var input unsafe.Pointer
	if len(data) > 0 {
		input = C.CBytes(data)
		defer C.free(input)
	}
	var output unsafe.Pointer
	var length C.int
	status := C.vault(C.int(action), account, input, C.int(len(data)), &output, &length)
	if status == C.errSecItemNotFound {
		return nil, ErrNoCredentials
	}
	if status != C.errSecSuccess {
		return nil, fmt.Errorf("protected storage unavailable (%d)", status)
	}
	if output == nil {
		return nil, nil
	}
	defer C.free(output)
	return C.GoBytes(output, length), nil
}
func (v keychainVault) Load() ([]byte, error)  { return v.operate(0, nil) }
func (v keychainVault) Save(data []byte) error { _, err := v.operate(1, data); return err }
func (v keychainVault) Delete() error          { _, err := v.operate(2, nil); return err }
