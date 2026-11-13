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

// ---- Go -> Java ---------------------------------------------------------
// The Wails runtime keeps its own JavaVM/bridge references private, so
// Skein caches its own: MainActivity calls nativeInitSkeinBridge from
// onCreate (main thread, where FindClass resolves app classes), and
// every Java->Go entry below refreshes the JVM pointer as well.
// skeinActivity is a global reference to the MainActivity instance;
// the Go side calls its instance methods (requestVpnPermission,
// vpnPermissionGranted, openVpnSettings, startVpnService,
// stopVpnService), which post to the main thread where needed.
static JavaVM* skeinJvm = NULL;
static jobject skeinActivity = NULL;
static jclass skeinActivityClass = NULL;

static void skeinStoreJvm(JNIEnv *env) {
	if (skeinJvm == NULL) (*env)->GetJavaVM(env, &skeinJvm);
}

static void skeinStoreActivity(JNIEnv *env, jobject activity) {
	skeinStoreJvm(env);
	if (skeinActivity != NULL) return;
	skeinActivity = (*env)->NewGlobalRef(env, activity);
	jclass cls = (*env)->GetObjectClass(env, activity);
	if (cls != NULL) {
		skeinActivityClass = (*env)->NewGlobalRef(env, cls);
		(*env)->DeleteLocalRef(env, cls);
	}
}

static JNIEnv* skeinGetEnv(int *needsDetach) {
	if (skeinJvm == NULL) return NULL;
	JNIEnv* env = NULL;
	jint r = (*skeinJvm)->GetEnv(skeinJvm, (void**)&env, JNI_VERSION_1_6);
	if (r == JNI_EDETACHED) {
		if ((*skeinJvm)->AttachCurrentThread(skeinJvm, &env, NULL) != 0) return NULL;
		*needsDetach = 1;
	} else if (r != JNI_OK) {
		return NULL;
	}
	return env;
}

static void skeinReleaseEnv(int needsDetach) {
	if (needsDetach && skeinJvm != NULL) (*skeinJvm)->DetachCurrentThread(skeinJvm);
}

static void skeinClearException(JNIEnv *env, const char *where) {
	if ((*env)->ExceptionCheck(env)) {
		(*env)->ExceptionDescribe(env);
		(*env)->ExceptionClear(env);
	}
}

// Call a `void name()` instance method on the cached MainActivity.
// Returns 0 on success, -1 when no activity is cached, -2 when the
// method is missing or threw.
static int skeinCallActivityVoid(const char *method) {
	if (skeinActivity == NULL || skeinActivityClass == NULL) return -1;
	int detach = 0;
	JNIEnv* env = skeinGetEnv(&detach);
	if (env == NULL) return -1;
	int rc = -2;
	jmethodID mid = (*env)->GetMethodID(env, skeinActivityClass, method, "()V");
	if (mid != NULL) {
		(*env)->CallVoidMethod(env, skeinActivity, mid);
		if ((*env)->ExceptionCheck(env)) {
			skeinClearException(env, method);
		} else {
			rc = 0;
		}
	} else {
		skeinClearException(env, method);
	}
	skeinReleaseEnv(detach);
	return rc;
}

// Call a `boolean name()` instance method on the cached MainActivity.
// Returns 1/0 for the value, -1 when the call could not be made.
static int skeinCallActivityBool(const char *method) {
	if (skeinActivity == NULL || skeinActivityClass == NULL) return -1;
	int detach = 0;
	JNIEnv* env = skeinGetEnv(&detach);
	if (env == NULL) return -1;
	int rc = -1;
	jmethodID mid = (*env)->GetMethodID(env, skeinActivityClass, method, "()Z");
	if (mid != NULL) {
		jboolean v = (*env)->CallBooleanMethod(env, skeinActivity, mid);
		if ((*env)->ExceptionCheck(env)) {
			skeinClearException(env, method);
		} else {
			rc = v ? 1 : 0;
		}
	} else {
		skeinClearException(env, method);
	}
	skeinReleaseEnv(detach);
	return rc;
}
// Call SkeinVpnService.protectSocket(fd): protect the daemon's
// underlay socket from the VPN so its packets bypass the TUN.
// Returns 1 when protected, 0 when no service is running (or the
// service declined), -1 when the call could not be made.
static int skeinProtectSocket(int fd) {
	int detach = 0;
	JNIEnv* env = skeinGetEnv(&detach);
	if (env == NULL) return -1;
	int rc = -1;
	jclass cls = (*env)->FindClass(env, "com/skein/app/SkeinVpnService");
	if (cls != NULL) {
		jmethodID mid = (*env)->GetStaticMethodID(env, cls, "protectSocket", "(I)Z");
		if (mid != NULL) {
			jboolean v = (*env)->CallStaticBooleanMethod(env, cls, mid, (jint)fd);
			if ((*env)->ExceptionCheck(env)) {
				skeinClearException(env, "protectSocket");
			} else {
				rc = v ? 1 : 0;
			}
		} else {
			skeinClearException(env, "protectSocket");
		}
		(*env)->DeleteLocalRef(env, cls);
	} else {
		skeinClearException(env, "protectSocket");
	}
	skeinReleaseEnv(detach);
	return rc;
}

// Method names for the MainActivity calls below.
// NOTE: these MUST be functions returning a string literal, not
// variables. A `static const char *kMethod...` variable in the cgo
// preamble crashes the app on launch: the Go code references it as
// C.kMethod* from another translation unit, and the dynamic linker
// cannot resolve a static (internal-linkage) data symbol at load
// time ("dlopen failed: cannot locate symbol kMethodOpenVpnSettings").
// Dropping `static` does not work either — the preamble is compiled
// into several object files, so a non-static variable becomes a
// duplicate symbol at link time. A static *function* is safe: each
// translation unit gets its own copy and the call resolves at static
// link time, exactly like skeinCallActivityVoid above.
static const char *skeinMethodRequestVpn(void) { return "requestVpnPermission"; }
static const char *skeinMethodVpnGranted(void) { return "vpnPermissionGranted"; }
static const char *skeinMethodOpenVpnSettings(void) { return "openVpnSettings"; }
static const char *skeinMethodStartVpn(void) { return "startVpnService"; }
static const char *skeinMethodStopVpn(void) { return "stopVpnService"; }
*/
import "C"

import (
	"context"
	"log"
	"time"
	"unsafe"

	"github.com/Utsav-X-bit/skein/gui/services"
	"github.com/Utsav-X-bit/skein/internal/android"
)

// requestVpnPermission asks the Java side to launch the system VPN
// consent dialog (VpnService.prepare()). The outcome arrives
// asynchronously via nativeVpnPermissionResult below.
func requestVpnPermission() int { return int(C.skeinCallActivityVoid(C.skeinMethodRequestVpn())) }

// vpnPermissionGranted asks Android live whether Skein is already
// authorised (VpnService.prepare() returns null). It returns
// (granted, ok): ok is false when the bridge is not ready.
func vpnPermissionGranted() (bool, bool) {
	switch rc := int(C.skeinCallActivityBool(C.skeinMethodVpnGranted())); rc {
	case 1:
		return true, true
	case 0:
		return false, true
	default:
		return false, false
	}
}

func openVpnSettings() int { return int(C.skeinCallActivityVoid(C.skeinMethodOpenVpnSettings())) }
func startVpnService() int { return int(C.skeinCallActivityVoid(C.skeinMethodStartVpn())) }
func stopVpnService() int  { return int(C.skeinCallActivityVoid(C.skeinMethodStopVpn())) }

// protectSocketFD asks the running SkeinVpnService to protect the
// given socket FD from the VPN (VpnService.protect). It reports
// whether the socket is protected; false means no service is running
// yet (the daemon starts before the service) or the call failed, in
// which case the socket is left unprotected and the dial may loop
// into the TUN.
func protectSocketFD(fd int) bool {
	return int(C.skeinProtectSocket(C.int(fd))) == 1
}

//export Java_com_skein_app_SkeinVpnService_nativeSetTunFd
func Java_com_skein_app_SkeinVpnService_nativeSetTunFd(env *C.JNIEnv, thiz C.jobject, fd C.jint) C.jint {
	C.skeinStoreJvm(env)
	if err := android.SetTunFd(int(fd)); err != nil {
		log.Printf("skein: android: setTunFd: %v", err)
		return -1
	}
	return 0
}

//export Java_com_skein_app_SkeinVpnService_nativeStartDaemon
func Java_com_skein_app_SkeinVpnService_nativeStartDaemon(env *C.JNIEnv, thiz C.jobject, filesDir C.jstring) C.jint {
	C.skeinStoreJvm(env)
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
		var nullStr C.jstring // untyped nil is not a valid C.jstring
		return nullStr
	}
	cstr := C.CString(string(raw))
	defer C.free(unsafe.Pointer(cstr))
	return C.skeinNewJstring(env, cstr)
}

// ---- MainActivity natives (B1) ---------------------------------------
// MainActivity calls nativeInitSkeinBridge from onCreate so the Go
// side learns the JVM and the Activity instance (and the app-private
// files dir) before any VPN flow runs. The activity reference is
// cached as a JNI global ref in C; it is the same Activity for the
// life of the app process.

//export Java_com_skein_app_MainActivity_nativeInitSkeinBridge
func Java_com_skein_app_MainActivity_nativeInitSkeinBridge(env *C.JNIEnv, thiz C.jobject, filesDir C.jstring) {
	C.skeinStoreActivity(env, thiz)
	cstr := C.skeinJstringChars(env, filesDir)
	if cstr == nil {
		log.Printf("skein: android: initBridge: missing files dir")
		return
	}
	dir := C.GoString(cstr)
	C.skeinReleaseJstringChars(env, filesDir, cstr)
	if err := android.SetFilesDir(dir); err != nil {
		log.Printf("skein: android: initBridge: %v", err)
	}
}

//export Java_com_skein_app_MainActivity_nativeVpnPermissionResult
func Java_com_skein_app_MainActivity_nativeVpnPermissionResult(env *C.JNIEnv, thiz C.jobject, granted C.jboolean) {
	C.skeinStoreJvm(env)
	services.NoteVpnPermissionResult(granted != 0)
}
