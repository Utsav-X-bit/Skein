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
#include <string.h>
#include <pthread.h>

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
// Guards skeinActivity/skeinActivityClass: nativeInitSkeinBridge replaces
// them on every Activity onCreate (Android can recreate MainActivity while
// the process and the Go runtime live on), and a caller must never use a
// reference the rebind just deleted.
static pthread_mutex_t skeinActivityMu = PTHREAD_MUTEX_INITIALIZER;
// Cached SkeinVpnService class (global ref). FindClass does NOT
// work from a Go goroutine attached via AttachCurrentThread — the
// attached thread has no app class loader — so the class is cached
// here from a Java thread (nativeStartDaemon) and skeinProtectSocket
// uses the cached ref instead of FindClass.
static jclass skeinVpnServiceClass = NULL;

static void skeinStoreJvm(JNIEnv *env) {
	if (skeinJvm == NULL) (*env)->GetJavaVM(env, &skeinJvm);
}

// Cache the SkeinVpnService class for skeinProtectSocket. Must be
// called from a Java thread (a native method entry), where FindClass
// resolves app classes; a no-op once cached.
static void skeinStoreVpnServiceClass(JNIEnv *env) {
	if (skeinVpnServiceClass != NULL) return;
	jclass cls = (*env)->FindClass(env, "com/skein/app/SkeinVpnService");
	if (cls != NULL) {
		skeinVpnServiceClass = (*env)->NewGlobalRef(env, cls);
		(*env)->DeleteLocalRef(env, cls);
	}
}

static void skeinStoreActivity(JNIEnv *env, jobject activity) {
	skeinStoreJvm(env);
	// Always replace: a recreated Activity must not leave the Go side
	// calling methods on the destroyed instance (the VPN dialog and the
	// service start would silently never happen).
	jobject fresh = (*env)->NewGlobalRef(env, activity);
	jclass freshClass = NULL;
	jclass cls = (*env)->GetObjectClass(env, activity);
	if (cls != NULL) {
		freshClass = (*env)->NewGlobalRef(env, cls);
		(*env)->DeleteLocalRef(env, cls);
	}
	pthread_mutex_lock(&skeinActivityMu);
	jobject old = skeinActivity;
	jclass oldClass = skeinActivityClass;
	skeinActivity = fresh;
	skeinActivityClass = freshClass;
	pthread_mutex_unlock(&skeinActivityMu);
	// Safe to free now: callers hold local references (skeinAcquireActivity).
	if (old != NULL) (*env)->DeleteGlobalRef(env, old);
	if (oldClass != NULL) (*env)->DeleteGlobalRef(env, oldClass);
}

// Take local references to the cached activity and its class. Returns 0
// when nothing is cached; the caller must DeleteLocalRef both when done.
static int skeinAcquireActivity(JNIEnv *env, jobject *act, jclass *cls) {
	*act = NULL;
	*cls = NULL;
	pthread_mutex_lock(&skeinActivityMu);
	if (skeinActivity != NULL) *act = (*env)->NewLocalRef(env, skeinActivity);
	if (skeinActivityClass != NULL) *cls = (*env)->NewLocalRef(env, skeinActivityClass);
	pthread_mutex_unlock(&skeinActivityMu);
	if (*act == NULL || *cls == NULL) {
		if (*act != NULL) (*env)->DeleteLocalRef(env, *act);
		if (*cls != NULL) (*env)->DeleteLocalRef(env, *cls);
		*act = NULL;
		*cls = NULL;
		return 0;
	}
	return 1;
}

static void skeinReleaseActivity(JNIEnv *env, jobject act, jclass cls) {
	if (act != NULL) (*env)->DeleteLocalRef(env, act);
	if (cls != NULL) (*env)->DeleteLocalRef(env, cls);
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
	int detach = 0;
	JNIEnv* env = skeinGetEnv(&detach);
	if (env == NULL) return -1;
	jobject act;
	jclass cls;
	int rc = -1;
	if (skeinAcquireActivity(env, &act, &cls)) {
		rc = -2;
		jmethodID mid = (*env)->GetMethodID(env, cls, method, "()V");
		if (mid != NULL) {
			(*env)->CallVoidMethod(env, act, mid);
			if ((*env)->ExceptionCheck(env)) {
				skeinClearException(env, method);
			} else {
				rc = 0;
			}
		} else {
			skeinClearException(env, method);
		}
		skeinReleaseActivity(env, act, cls);
	}
	skeinReleaseEnv(detach);
	return rc;
}

// Call a `boolean name()` instance method on the cached MainActivity.
// Returns 1/0 for the value, -1 when the call could not be made.
static int skeinCallActivityBool(const char *method) {
	int detach = 0;
	JNIEnv* env = skeinGetEnv(&detach);
	if (env == NULL) return -1;
	jobject act;
	jclass cls;
	int rc = -1;
	if (skeinAcquireActivity(env, &act, &cls)) {
		jmethodID mid = (*env)->GetMethodID(env, cls, method, "()Z");
		if (mid != NULL) {
			jboolean v = (*env)->CallBooleanMethod(env, act, mid);
			if ((*env)->ExceptionCheck(env)) {
				skeinClearException(env, method);
			} else {
				rc = v ? 1 : 0;
			}
		} else {
			skeinClearException(env, method);
		}
		skeinReleaseActivity(env, act, cls);
	}
	skeinReleaseEnv(detach);
	return rc;
}
// Call a `void name(boolean)` instance method on the cached MainActivity.
// Returns 0 on success, -1 when no activity is cached, -2 when the method
// is missing or threw.
static int skeinCallActivityVoidBool(const char *method, int value) {
	int detach = 0;
	JNIEnv* env = skeinGetEnv(&detach);
	if (env == NULL) return -1;
	jobject act;
	jclass cls;
	int rc = -1;
	if (skeinAcquireActivity(env, &act, &cls)) {
		rc = -2;
		jmethodID mid = (*env)->GetMethodID(env, cls, method, "(Z)V");
		if (mid != NULL) {
			(*env)->CallVoidMethod(env, act, mid, value ? JNI_TRUE : JNI_FALSE);
			if ((*env)->ExceptionCheck(env)) {
				skeinClearException(env, method);
			} else {
				rc = 0;
			}
		} else {
			skeinClearException(env, method);
		}
		skeinReleaseActivity(env, act, cls);
	}
	skeinReleaseEnv(detach);
	return rc;
}

// Call a `String name()` instance method on the cached MainActivity.
// Returns a malloc'd copy (free() it), or NULL when no activity is cached
// or the call failed.
static char *skeinCallActivityString(const char *method) {
	int detach = 0;
	JNIEnv* env = skeinGetEnv(&detach);
	if (env == NULL) return NULL;
	char *out = NULL;
	jobject act;
	jclass cls;
	if (skeinAcquireActivity(env, &act, &cls)) {
		jmethodID mid = (*env)->GetMethodID(env, cls, method, "()Ljava/lang/String;");
		if (mid != NULL) {
			jstring s = (jstring)(*env)->CallObjectMethod(env, act, mid);
			if ((*env)->ExceptionCheck(env)) {
				skeinClearException(env, method);
			} else if (s != NULL) {
				const char *chars = (*env)->GetStringUTFChars(env, s, NULL);
				if (chars != NULL) {
					out = strdup(chars);
					(*env)->ReleaseStringUTFChars(env, s, chars);
				}
				(*env)->DeleteLocalRef(env, s);
			}
		} else {
			skeinClearException(env, method);
		}
		skeinReleaseActivity(env, act, cls);
	}
	skeinReleaseEnv(detach);
	return out;
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
	if (skeinVpnServiceClass != NULL) {
		jmethodID mid = (*env)->GetStaticMethodID(env, skeinVpnServiceClass, "protectSocket", "(I)Z");
		if (mid != NULL) {
			jboolean v = (*env)->CallStaticBooleanMethod(env, skeinVpnServiceClass, mid, (jint)fd);
			if ((*env)->ExceptionCheck(env)) {
				skeinClearException(env, "protectSocket");
			} else {
				rc = v ? 1 : 0;
			}
		} else {
			skeinClearException(env, "protectSocket");
		}
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
static const char *skeinMethodLocalNetworkInfo(void) { return "localNetworkInfo"; }
static const char *skeinMethodSetHighPerfWifi(void) { return "setHighPerformanceWifi"; }
static const char *skeinMethodHighPerfWifi(void) { return "highPerformanceWifiEnabled"; }
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"time"
	"unsafe"

	"github.com/Utsav-X-bit/skein/gui/services"
	"github.com/Utsav-X-bit/skein/internal/android"
	"github.com/Utsav-X-bit/skein/internal/daemon"
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

// setHighPerformanceWifi persists the high-performance Wi-Fi preference and
// applies it to a running tunnel. Returns 0 on success.
func setHighPerformanceWifi(enabled bool) int {
	v := C.int(0)
	if enabled {
		v = 1
	}
	return int(C.skeinCallActivityVoidBool(C.skeinMethodSetHighPerfWifi(), v))
}

// highPerformanceWifiEnabled reads the persisted preference. ok is false
// when the bridge is not ready, which is not the same as "off".
func highPerformanceWifiEnabled() (bool, bool) {
	switch rc := int(C.skeinCallActivityBool(C.skeinMethodHighPerfWifi())); rc {
	case 1:
		return true, true
	case 0:
		return false, true
	default:
		return false, false
	}
}

// androidNetInfo is the underlay inventory MainActivity.localNetworkInfo
// reports: the device's own addresses and the interfaces carrying them.
type androidNetInfo struct {
	Addresses  []string `json:"addresses"`
	Interfaces []struct {
		Name         string `json:"name"`
		Index        int    `json:"index"`
		MTU          int    `json:"mtu"`
		HardwareAddr string `json:"hardware_addr"`
		Up           bool   `json:"up"`
		Multicast    bool   `json:"multicast"`
		Loopback     bool   `json:"loopback"`
		PointToPoint bool   `json:"point_to_point"`
	} `json:"interfaces"`
	Error string `json:"error"`
}

// androidLocalNetworkInfo asks Java for the underlay inventory. Android
// 11+ denies net.Interfaces() to apps, so neither the addresses nor the
// multicast-capable interfaces can be read from Go; discovery would find
// no LAN candidate and every node would advertise itself unreachable.
func androidLocalNetworkInfo() (androidNetInfo, error) {
	var info androidNetInfo
	cstr := C.skeinCallActivityString(C.skeinMethodLocalNetworkInfo())
	if cstr == nil {
		return info, errors.New("android: local network info: the app UI is not ready")
	}
	defer C.free(unsafe.Pointer(cstr))
	if err := json.Unmarshal([]byte(C.GoString(cstr)), &info); err != nil {
		return info, fmt.Errorf("android: local network info: %w", err)
	}
	if info.Error != "" {
		return info, fmt.Errorf("android: local network info: %s", info.Error)
	}
	return info, nil
}

// androidLocalAddresses reports the device's underlay addresses. Eligibility
// (private/global, dedupe) is internal/path's job.
func androidLocalAddresses() ([]netip.Addr, error) {
	info, err := androidLocalNetworkInfo()
	if err != nil {
		return nil, err
	}
	addrs := make([]netip.Addr, 0, len(info.Addresses))
	for _, literal := range info.Addresses {
		if addr, err := netip.ParseAddr(literal); err == nil {
			addrs = append(addrs, addr)
		}
	}
	return addrs, nil
}

// androidLocalInterfaces reports the interfaces behind those addresses, for
// multicast joins and the resolver's interface selection.
func androidLocalInterfaces() ([]net.Interface, error) {
	info, err := androidLocalNetworkInfo()
	if err != nil {
		return nil, err
	}
	ifaces := make([]net.Interface, 0, len(info.Interfaces))
	for _, entry := range info.Interfaces {
		if entry.Index <= 0 {
			// A zero index means the join would silently target no
			// interface; report it instead of returning a list that
			// looks usable.
			return nil, fmt.Errorf("android: interface %q has no usable index (%d)", entry.Name, entry.Index)
		}
		var flags net.Flags
		if entry.Up {
			flags |= net.FlagUp
		}
		if entry.Multicast {
			flags |= net.FlagMulticast
		}
		if entry.Loopback {
			flags |= net.FlagLoopback
		}
		if entry.PointToPoint {
			flags |= net.FlagPointToPoint
		}
		var hardware net.HardwareAddr
		if entry.HardwareAddr != "" {
			if parsed, err := net.ParseMAC(entry.HardwareAddr); err == nil {
				hardware = parsed
			}
		}
		ifaces = append(ifaces, net.Interface{
			Index:        entry.Index,
			MTU:          entry.MTU,
			Name:         entry.Name,
			HardwareAddr: hardware,
			Flags:        flags,
		})
	}
	return ifaces, nil
}

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
	C.skeinStoreVpnServiceClass(env)
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
	conn, err := android.Connect(ctx)
	if err != nil {
		// A conflict means another actor already moved the connection:
		// report the real state instead of failing a connected tunnel.
		if conn.State == daemon.ConnConnected {
			return 0
		}
		log.Printf("skein: android: connectDaemon: %v", err)
		return -1
	}
	// Connect returns a nil error for any settled state, ConnError
	// included. The service must not report "Connected" for a dataplane
	// that failed to come up: it tears down and surfaces this instead.
	if conn.State != daemon.ConnConnected {
		log.Printf("skein: android: connectDaemon: settled in state %q: %s", conn.State, conn.LastError)
		return -2
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

//export Java_com_skein_app_SkeinVpnService_nativeVpnFailure
func Java_com_skein_app_SkeinVpnService_nativeVpnFailure(env *C.JNIEnv, thiz C.jobject, reason C.jstring) {
	C.skeinStoreJvm(env)
	cstr := C.skeinJstringChars(env, reason)
	if cstr == nil {
		return
	}
	msg := C.GoString(cstr)
	C.skeinReleaseJstringChars(env, reason, cstr)
	services.NoteVpnError(msg)
}

// ---- MainActivity natives (B1) ---------------------------------------
// MainActivity calls nativeInitSkeinBridge from onCreate so the Go
// side learns the JVM and the Activity instance (and the app-private
// files dir) before any VPN flow runs. The activity reference is a JNI
// global ref replaced on every onCreate: Android may recreate the
// Activity while this process (and the Go runtime) keeps running, and
// the Go side must talk to the live instance, not the destroyed one.

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
