//go:build android

// JNI bridge for SkeinVpnService: the Java service calls these native
// methods (implemented in libwails.so, already loaded by
// WailsBridge) to drive the in-process daemon (internal/android).
// The methods are plain JNI exports: Java declares them native in
// SkeinVpnService with matching signatures, and the VM resolves them
// by name (Java_com_skein_app_SkeinVpnService_<method>).
//
// The Go runtime is started by Wails (nativeInit) before any of these
// can run; the service is only ever started from the app UI, so that
// holds. All methods are safe to call from a Java worker thread;
// nativeConnectDaemon / nativeDisconnectDaemon can block for seconds
// (daemon.Connect polls up to 30s), so Java must NOT call them on
// the main thread.
//
// Status codes: 0 is success, negative is failure (details in logcat).
package main

/*
#include <jni.h>
#include <stdlib.h>

static const char *skeinJstringChars(JNIEnv *env, jstring s) {
	if (s == NULL) return NULL;
	return (*env)->GetStringUTFChars(env, s, NULL);
}

static void skeinReleaseJstringChars(JNIEnv *env, jstring s, const char *c) {
	if (s != NULL && c != NULL) (*env)->ReleaseStringUTFChars(env, s, c);
}

static jstring skeinNewJstring(JNIEnv *env, const char *c) {
	if (c == NULL) return NULL;
	return (*env)->NewStringUTF(env, c);
}
*/
import "C"

import (
	"context"
	"log"
	"time"
	"unsafe"

	"github.com/Utsav-X-bit/skein/internal/android"
)

//export Java_com_skein_app_SkeinVpnService_nativeSetTunFd
func Java_com_skein_app_SkeinVpnService_nativeSetTunFd(env *C.JNIEnv, thiz C.jobject, fd C.jint) C.jint {
	if err := android.SetTunFd(int(fd)); err != nil {
		log.Printf("skein: android: setTunFd: %v", err)
		return -1
	}
	return 0
}

//export Java_com_skein_app_SkeinVpnService_nativeStartDaemon
func Java_com_skein_app_SkeinVpnService_nativeStartDaemon(env *C.JNIEnv, thiz C.jobject, filesDir C.jstring) C.jint {
	cstr := C.skeinJstringChars(env, filesDir)
	if cstr == nil {
		log.Printf("skein: android: startDaemon: missing files dir")
		return -1
	}
	dir := C.GoString(cstr)
	C.skeinReleaseJstringChars(env, filesDir, cstr)
	if err := android.SetFilesDir(dir); err != nil {
		log.Printf("skein: android: startDaemon: %v", err)
		return -2
	}
	if err := android.Start(); err != nil {
		log.Printf("skein: android: startDaemon: %v", err)
		return -3
	}
	return 0
}

//export Java_com_skein_app_SkeinVpnService_nativeStopDaemon
func Java_com_skein_app_SkeinVpnService_nativeStopDaemon(env *C.JNIEnv, thiz C.jobject) C.jint {
	if err := android.Stop(); err != nil {
		log.Printf("skein: android: stopDaemon: %v", err)
		return -1
	}
	return 0
}

//export Java_com_skein_app_SkeinVpnService_nativeConnectDaemon
func Java_com_skein_app_SkeinVpnService_nativeConnectDaemon(env *C.JNIEnv, thiz C.jobject) C.jint {
	// daemon.Connect polls up to 30s for the connection to settle;
	// the Java caller must run this off the main thread.
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	if _, err := android.Connect(ctx); err != nil {
		log.Printf("skein: android: connectDaemon: %v", err)
		return -1
	}
	return 0
}

//export Java_com_skein_app_SkeinVpnService_nativeDisconnectDaemon
func Java_com_skein_app_SkeinVpnService_nativeDisconnectDaemon(env *C.JNIEnv, thiz C.jobject) C.jint {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	if _, err := android.Disconnect(ctx); err != nil {
		log.Printf("skein: android: disconnectDaemon: %v", err)
		return -1
	}
	return 0
}

//export Java_com_skein_app_SkeinVpnService_nativeGetVpnConfig
func Java_com_skein_app_SkeinVpnService_nativeGetVpnConfig(env *C.JNIEnv, thiz C.jobject) C.jstring {
	raw, err := android.GetVpnConfig()
	if err != nil {
		log.Printf("skein: android: getVpnConfig: %v", err)
		return nil
	}
	cstr := C.CString(string(raw))
	defer C.free(unsafe.Pointer(cstr))
	return C.skeinNewJstring(env, cstr)
}
