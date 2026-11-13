package com.skein.app;

import com.wails.app.WailsBridge;

import android.annotation.SuppressLint;
import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.content.res.Configuration;
import android.database.Cursor;
import android.net.ConnectivityManager;
import android.net.LinkProperties;
import android.net.Network;
import android.net.NetworkCapabilities;
import android.net.NetworkRequest;
import android.net.Uri;
import android.os.BatteryManager;
import android.os.Build;
import android.os.Bundle;
import android.os.PowerManager;
import android.content.pm.PackageManager;
import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.provider.MediaStore;
import android.provider.OpenableColumns;
import android.provider.Settings;
import android.util.Base64;
import android.util.Log;
import android.webkit.ConsoleMessage;
import android.webkit.ValueCallback;
import android.webkit.WebChromeClient;
import android.webkit.WebChromeClient.FileChooserParams;
import android.webkit.WebResourceRequest;
import android.webkit.WebResourceResponse;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;

import androidx.activity.OnBackPressedCallback;
import androidx.annotation.Nullable;
import androidx.appcompat.app.AppCompatActivity;
import androidx.core.content.ContextCompat;
import androidx.core.content.FileProvider;
import androidx.webkit.WebViewAssetLoader;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.File;
import java.io.FileOutputStream;
import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.util.ArrayList;
import java.util.List;

/**
 * MainActivity hosts the WebView and manages the Wails application lifecycle.
 * It uses WebViewAssetLoader to serve assets from the Go library without
 * requiring a network server.
 */
public class MainActivity extends AppCompatActivity {
    private static final String TAG = "WailsActivity";
    private static final boolean DEBUG = BuildConfig.DEBUG;
    private static final String WAILS_SCHEME = "https";
    private static final String WAILS_HOST = "wails.localhost";
    private static final int FILE_PICKER_REQUEST = 7001;
    // System VPN consent dialog (VpnService.prepare()).
    private static final int VPN_REQUEST_CODE = 7005;

    // Set when startVpnService() is asked to start the VPN before the
    // system permission is granted: the permission dialog is shown
    // instead, and onActivityResult starts the service once the user
    // grants it. Without this, starting the service with no permission
    // makes establish() return null and the daemon stops at once.
    private boolean pendingStartVpn = false;

    // Back probe: close an open Radix overlay (dialog, alertdialog, sheet,
    // popover) by dispatching the Escape key it already listens for —
    // Android's Back is not an Escape key. Attribute values are unquoted so
    // the selector needs no escaping inside this Java string literal.
    private static final String BACK_PROBE_JS =
            "(function(){try{"
            + "var open=document.querySelector("
            + "'[role=dialog][data-state=open],[role=alertdialog][data-state=open],"
            + "[data-radix-popper-content-wrapper]');"
            + "if(!open)return '0';"
            + "document.dispatchEvent(new KeyboardEvent('keydown',"
            + "{key:'Escape',code:'Escape',bubbles:true,cancelable:true}));"
            + "return '1';}catch(e){return '0';}})()";

    private WebView webView;
    private Uri devServer;
    private WailsBridge bridge;
    // Battery: system-event receivers are registered only while the activity is
    // in the foreground (onStart) and torn down in onStop, so background battery/
    // network/screen broadcasts don't wake the app.
    private boolean systemReceiversRegistered = false;
    private WebViewAssetLoader assetLoader;

    // The Go-side dialog ID of the in-flight file picker (-1 when idle)
    private int pendingFilePickerCallbackID = -1;
    private static final int PHOTO_CAPTURE_REQUEST = 7002;
    private static final int VIDEO_CAPTURE_REQUEST = 7003;
    private static final int CAMERA_PERMISSION_REQUEST = 7010;
    // Android 13+ POST_NOTIFICATIONS (the VPN's persistent notification).
    private static final int NOTIFICATION_PERMISSION_REQUEST = 7011;

    // In-flight callback for an HTML <input type="file"> chooser opened by the
    // WebView's WebChromeClient (null when idle).
    private static final int WEBVIEW_FILE_CHOOSER_REQUEST = 7004;
    private ValueCallback<Uri[]> webViewFileChooserCallback;
    private File pendingCaptureFile;
    private boolean pendingCaptureIsVideo;

    // System-event sources (battery/power, screen lock, network). Registered in
    // onCreate, torn down in onDestroy. Each forwards a "system:*" event to JS
    // via the bridge.
    private BroadcastReceiver batteryReceiver;
    private BroadcastReceiver screenReceiver;
    private BroadcastReceiver powerSaveReceiver;
    private ConnectivityManager connectivityManager;
    private ConnectivityManager.NetworkCallback networkCallback;

    // Go natives implemented in libwails.so (gui/jni_android.go).
    private native void nativeInitSkeinBridge(String filesDir);
    private native void nativeVpnPermissionResult(boolean granted);

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_main);

        String devURL = WailsBridge.devServerURL(this);
        if (devURL != null) devServer = Uri.parse(devURL);

        // Initialize the native Go library
        bridge = new WailsBridge(this);
        bridge.initialize();

        // Hand Go the JVM/activity reference and the app-private files
        // dir before any VPN flow runs. Safe to call before the page
        // loads: it only caches references and records the dir.
        try {
            nativeInitSkeinBridge(getFilesDir().getAbsolutePath());
        } catch (UnsatisfiedLinkError e) {
            Log.w(TAG, "nativeInitSkeinBridge unavailable (desktop/dev build?)", e);
        }

        // Set up WebView
        setupWebView();

        // Back navigation. onBackPressed() is not a reliable hook: apps
        // targeting SDK 35+ get the platform back system by default
        // (predictive back), which goes through the OnBackPressedDispatcher
        // and never calls the legacy override — so Back exited the app
        // instead of walking the page history on Android 15/16.
        //
        // Order: close an open overlay, then walk the hash-route history,
        // then leave the app. Overlays (Radix dialogs, sheets, popovers)
        // are not history entries, so without the first step Back navigated
        // the route underneath an open sheet — or finished the activity
        // with it still on screen.
        getOnBackPressedDispatcher().addCallback(this, new OnBackPressedCallback(true) {
            @Override
            public void handleOnBackPressed() {
                final WebView view = webView;
                if (view == null) {
                    finish();
                    return;
                }
                try {
                    view.evaluateJavascript(BACK_PROBE_JS, value -> {
                        // "1" means an overlay took the Escape and closed.
                        if ("\"1\"".equals(value)) {
                            return;
                        }
                        if (webView != null && webView.canGoBack()) {
                            webView.goBack();
                            return;
                        }
                        // Root of the page history: leave the app. The VPN
                        // service (and its notification) keep running.
                        finish();
                    });
                } catch (RuntimeException e) {
                    Log.w(TAG, "back probe failed", e);
                    finish();
                }
            }
        });

        // Load the application
        loadApplication();
    }

    @SuppressLint("SetJavaScriptEnabled")
    private void setupWebView() {
        webView = findViewById(R.id.webview);
        bridge.setWebView(webView);

        // Configure WebView settings
        WebSettings settings = webView.getSettings();
        settings.setJavaScriptEnabled(true);
        settings.setDomStorageEnabled(true);
        settings.setDatabaseEnabled(true);
        settings.setAllowFileAccess(false);
        settings.setAllowContentAccess(false);
        settings.setMediaPlaybackRequiresUserGesture(false);
        settings.setMixedContentMode(WebSettings.MIXED_CONTENT_NEVER_ALLOW);

        // Enable debugging in debug builds
        if (DEBUG) {
            WebView.setWebContentsDebuggingEnabled(true);
        }

        // Set up asset loader for serving local assets
        assetLoader = new WebViewAssetLoader.Builder()
                .setDomain(WAILS_HOST)
                .addPathHandler("/", new WailsPathHandler(bridge))
                .build();

        // Set up WebView client to intercept requests
        webView.setWebViewClient(new WebViewClient() {
            @Nullable
            @Override
            public WebResourceResponse shouldInterceptRequest(WebView view, WebResourceRequest request) {
                // Handle wails.localhost requests
                if (request.getUrl().getHost() != null &&
                        (request.getUrl().getHost().equals(WAILS_HOST) ||
                         (devServer != null && request.getUrl().getScheme().equals(devServer.getScheme()) &&
                          request.getUrl().getAuthority().equals(devServer.getAuthority())))) {

                    // For wails API calls (runtime, capabilities, etc.) pass the
                    // full URL including the query string, because
                    // WebViewAssetLoader.PathHandler strips query params
                    String path = request.getUrl().getPath();
                    if (path != null && path.startsWith("/wails/")) {
                        String fullPath = path;
                        String query = request.getUrl().getQuery();
                        if (query != null && !query.isEmpty()) {
                            fullPath = path + "?" + query;
                        }
                        if (DEBUG) Log.d(TAG, "Wails API call: " + fullPath);

                        byte[] data = bridge.serveAsset(fullPath, request.getMethod(), "{}");
                        if (data != null && data.length > 0) {
                            java.io.InputStream inputStream = new java.io.ByteArrayInputStream(data);
                            java.util.Map<String, String> headers = new java.util.HashMap<>();
                            headers.put("Access-Control-Allow-Origin", "*");
                            headers.put("Cache-Control", "no-cache");
                            headers.put("Content-Type", "application/json");

                            // Detect error envelopes: if the body starts with
                            // {"ok":false, return HTTP 500 so the fallback
                            // transport's fetch() rejects the promise.
                            int status = 200;
                            String reason = "OK";
                            if (data.length > 12) {
                                String prefix = new String(data, 0, Math.min(data.length, 20),
                                        java.nio.charset.StandardCharsets.UTF_8);
                                if (prefix.contains("\"ok\":false")) {
                                    status = 500;
                                    reason = "Internal Error";
                                }
                            }

                            return new WebResourceResponse(
                                "application/json",
                                "UTF-8",
                                status,
                                reason,
                                headers,
                                inputStream
                            );
                        }
                        // Return error response if data is null
                        return new WebResourceResponse(
                            "application/json",
                            "UTF-8",
                            500,
                            "Internal Error",
                            new java.util.HashMap<>(),
                            new java.io.ByteArrayInputStream("{}".getBytes())
                        );
                    }

                    // Stream captured photos/videos from the cache with HTTP Range
                    // support so <video> can seek/stream a clip of any length.
                    if (path != null && path.startsWith("/__capture__/")) {
                        return serveCaptureFile(path.substring("/__capture__/".length()), request);
                    }

                    // Vite serves its own HTTP headers and WebSocket endpoint.
                    if (devServer != null && !request.getUrl().getHost().equals(WAILS_HOST)) {
                        return super.shouldInterceptRequest(view, request);
                    }

                    // For regular assets, use the asset loader
                    return assetLoader.shouldInterceptRequest(request.getUrl());
                }

                return super.shouldInterceptRequest(view, request);
            }

            @Override
            public void onPageFinished(WebView view, String url) {
                super.onPageFinished(view, url);
                if (DEBUG) Log.d(TAG, "Page loaded: " + url);
                bridge.onPageFinished(url);
                // Now that JS listeners are mounted, push a snapshot of the
                // current battery / network / theme so the UI starts populated.
                emitSystemSnapshot();
            }
        });

        // Add JavaScript interface for Go communication
        webView.addJavascriptInterface(new WailsJSBridge(bridge, webView), "wails");

        // A WebChromeClient is required for HTML <input type="file"> elements to
        // work; without onShowFileChooser the WebView silently ignores clicks on
        // file inputs. It also forwards JS console messages to logcat for
        // debugging. Note: in-page permission prompts (geolocation, getUserMedia)
        // require an onPermissionRequest override which is not provided here;
        // those requests will be denied by the default implementation until a
        // scoped permissions handler is added.
        webView.setWebChromeClient(new WebChromeClient() {
            @Override
            public boolean onConsoleMessage(ConsoleMessage msg) {
                if (DEBUG) {
                    Log.d(TAG, "[WebView] " + msg.messageLevel() + ": " + msg.message()
                            + " (" + msg.sourceId() + ":" + msg.lineNumber() + ")");
                }
                return true;
            }

            @Override
            public boolean onShowFileChooser(WebView view,
                                             ValueCallback<Uri[]> filePathCallback,
                                             FileChooserParams fileChooserParams) {
                // Cancel any previous, still-pending chooser before starting a new one.
                if (webViewFileChooserCallback != null) {
                    webViewFileChooserCallback.onReceiveValue(null);
                }
                webViewFileChooserCallback = filePathCallback;

                Intent intent = fileChooserParams.createIntent();
                try {
                    startActivityForResult(intent, WEBVIEW_FILE_CHOOSER_REQUEST);
                } catch (android.content.ActivityNotFoundException e) {
                    webViewFileChooserCallback = null;
                    filePathCallback.onReceiveValue(null);
                    return false;
                }
                return true;
            }
        });
    }

    private void loadApplication() {
        String url = devServer != null ? devServer.toString() + "/" : WAILS_SCHEME + "://" + WAILS_HOST + "/";
        if (DEBUG) Log.d(TAG, "Loading URL: " + url);
        webView.loadUrl(url);
    }

    // ---- Skein VPN permission flow (B1) ---------------------------------
    // Called from Go (gui/jni_android.go) through the cached activity
    // reference. All of these may arrive on a non-UI thread, so each
    // posts to the main thread where Android requires it.

    /**
     * Launch the system VPN consent dialog. If Skein is already
     * authorised, VpnService.prepare() returns null and the granted
     * result is reported at once; otherwise the dialog appears and
     * its outcome lands in onActivityResult, which reports it to Go
     * via nativeVpnPermissionResult (the frontend learns it as the
     * "vpn:permission" Wails event).
     */
    public void requestVpnPermission() {
        runOnUiThread(() -> {
            try {
                Intent intent = SkeinVpnService.prepareIntent(this);
                if (intent == null) {
                    nativeVpnPermissionResult(true);
                    // A start that was waiting on consent (Settings ->
                    // Start, or a grant that landed between the two
                    // prepare() calls) has nothing left to wait for.
                    resumePendingStart();
                    return;
                }
                startActivityForResult(intent, VPN_REQUEST_CODE);
            } catch (Exception e) {
                Log.e(TAG, "requestVpnPermission failed", e);
                nativeVpnPermissionResult(false);
            }
        });
    }

    /** Starts the VPN service if a start was deferred for consent. */
    private void resumePendingStart() {
        if (!pendingStartVpn) {
            return;
        }
        pendingStartVpn = false;
        startVpnService();
    }

    /** True when Skein is already authorised to run a VPN. */
    public boolean vpnPermissionGranted() {
        return SkeinVpnService.prepareIntent(this) == null;
    }

    /**
     * The device's underlay inventory as JSON:
     * {@code {"addresses":["10.0.0.5",...],"interfaces":[{"name","index",
     * "mtu","hardware_addr","up","multicast","loopback","point_to_point"}]}},
     * or {@code {"error":"..."}} when it could not be read.
     *
     * <p>The Go daemon cannot enumerate any of this itself: Android 11+
     * denies {@code net.Interfaces()} (and therefore {@code
     * net.Interfaces()} on the Go side) to apps, so discovery finds no
     * host address and no multicast-capable interface, and peers never
     * learn a LAN candidate. Only non-VPN networks are read — with Skein
     * up, the app's default network is our own TUN, whose addresses are
     * overlay addresses no peer can dial. Per-interface lookup by name is
     * still permitted, which is why the inventory is built from
     * ConnectivityManager and resolved with {@code getByName}.
     *
     * <p>Zone ids are stripped; eligibility (private/global, dedupe) is
     * internal/path's job. Called from a Go worker thread (JNI).
     */
    public String localNetworkInfo() {
        JSONObject out = new JSONObject();
        JSONArray addresses = new JSONArray();
        JSONArray interfaces = new JSONArray();
        try {
            java.util.Set<String> seenAddresses = new java.util.LinkedHashSet<>();
            java.util.Set<String> seenInterfaces = new java.util.LinkedHashSet<>();
            for (LinkProperties props : underlayLinkProperties()) {
                for (android.net.LinkAddress link : props.getLinkAddresses()) {
                    java.net.InetAddress addr = link.getAddress();
                    if (addr == null || addr.isLoopbackAddress() || addr.isAnyLocalAddress()) {
                        continue;
                    }
                    String literal = addr.getHostAddress();
                    if (literal == null) {
                        continue;
                    }
                    int zone = literal.indexOf('%');
                    if (zone >= 0) {
                        literal = literal.substring(0, zone);
                    }
                    if (!literal.isEmpty() && seenAddresses.add(literal)) {
                        addresses.put(literal);
                    }
                }

                String name = props.getInterfaceName();
                if (name == null || name.isEmpty() || !seenInterfaces.add(name)) {
                    continue;
                }
                java.net.NetworkInterface nif = java.net.NetworkInterface.getByName(name);
                if (nif == null) {
                    // Report it: an interface the kernel lists but the app
                    // cannot resolve means the daemon cannot join multicast
                    // on it, and a silently empty list would look like "no
                    // LAN" instead of a broken lookup.
                    out.put("error", "NetworkInterface.getByName(\"" + name + "\") returned null");
                    return out.toString();
                }
                JSONObject entry = new JSONObject();
                entry.put("name", name);
                entry.put("index", nif.getIndex());
                entry.put("mtu", nif.getMTU());
                entry.put("up", nif.isUp());
                entry.put("multicast", nif.supportsMulticast());
                entry.put("loopback", nif.isLoopback());
                entry.put("point_to_point", nif.isPointToPoint());
                byte[] hardware = nif.getHardwareAddress();
                entry.put("hardware_addr", hardware == null ? "" : macLiteral(hardware));
                interfaces.put(entry);
            }
            out.put("addresses", addresses);
            out.put("interfaces", interfaces);
        } catch (Exception e) {
            Log.e(TAG, "localNetworkInfo failed", e);
            try {
                out.put("error", String.valueOf(e.getMessage()));
            } catch (Exception ignored) {
            }
        }
        return out.toString();
    }

    /** Link properties of the non-VPN networks, the default one first. */
    private List<LinkProperties> underlayLinkProperties() {
        List<LinkProperties> out = new ArrayList<>();
        ConnectivityManager cm =
                (ConnectivityManager) getSystemService(Context.CONNECTIVITY_SERVICE);
        if (cm == null) {
            return out;
        }
        List<Network> ordered = new ArrayList<>();
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
            Network active = cm.getActiveNetwork();
            if (active != null) {
                ordered.add(active);
            }
        }
        for (Network network : cm.getAllNetworks()) {
            ordered.add(network);
        }
        java.util.Set<Network> seen = new java.util.LinkedHashSet<>();
        for (Network network : ordered) {
            if (!seen.add(network)) {
                continue;
            }
            NetworkCapabilities caps = cm.getNetworkCapabilities(network);
            if (caps == null || caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN)) {
                continue;
            }
            LinkProperties props = cm.getLinkProperties(network);
            if (props != null) {
                out.add(props);
            }
        }
        return out;
    }

    /** Colon-separated hex, the form net.ParseMAC accepts. */
    private static String macLiteral(byte[] hardware) {
        StringBuilder sb = new StringBuilder(hardware.length * 3);
        for (int i = 0; i < hardware.length; i++) {
            if (i > 0) {
                sb.append(':');
            }
            sb.append(String.format(java.util.Locale.ROOT, "%02x", hardware[i]));
        }
        return sb.toString();
    }

    /** Open the system VPN settings so the user can re-enable Skein. */
    public void openVpnSettings() {
        runOnUiThread(() -> {
            try {
                Intent intent = new Intent(Settings.ACTION_VPN_SETTINGS);
                intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
                startActivity(intent);
            } catch (Exception e) {
                Log.e(TAG, "openVpnSettings failed", e);
            }
        });
    }

    /**
     * Start the SkeinVpnService (daemon start + TUN + connect). If the
     * system VPN permission is not granted yet, show the permission
     * dialog instead: establish() returns null without it, so starting
     * the service now would only make the daemon stop at once. The
     * service is started from onActivityResult once the user grants it.
     */
    public void startVpnService() {
        runOnUiThread(() -> {
            try {
                if (SkeinVpnService.prepareIntent(this) != null) {
                    pendingStartVpn = true;
                    requestVpnPermission();
                    return;
                }
                requestNotificationPermission();
                startServiceCompat(SkeinVpnService.connectIntent(this));
            } catch (Exception e) {
                Log.e(TAG, "startVpnService failed", e);
            }
        });
    }

    /** Stop the SkeinVpnService (disconnect + daemon stop). */
    public void stopVpnService() {
        runOnUiThread(() -> {
            try {
                startServiceCompat(SkeinVpnService.disconnectIntent(this));
            } catch (Exception e) {
                Log.e(TAG, "stopVpnService failed", e);
            }
        });
    }

    /**
     * Toggle the high-performance Wi-Fi lock (Settings → Connection). The
     * preference is persisted by the service and applied to a running tunnel
     * immediately, so no reconnect is needed.
     */
    public void setHighPerformanceWifi(boolean enabled) {
        try {
            SkeinVpnService.setHighPerformance(this, enabled);
        } catch (Exception e) {
            Log.e(TAG, "setHighPerformanceWifi failed", e);
        }
    }

    /** Read the persisted high-performance Wi-Fi preference. */
    public boolean highPerformanceWifiEnabled() {
        return SkeinVpnService.highPerformanceEnabled(this);
    }

    /**
     * Start a service that immediately calls startForeground. A plain
     * startService throws IllegalStateException for a backgrounded app
     * (Android 8+), which silently lost Disconnect taps; a foreground start
     * is allowed from the UI and from a notification action, and the
     * service handles both request kinds the same way. Falls back to
     * startService when the foreground start is refused (Android 12+ blocks
     * background foreground-service starts).
     */
    private void startServiceCompat(Intent intent) {
        try {
            ContextCompat.startForegroundService(this, intent);
        } catch (RuntimeException e) {
            Log.w(TAG, "startForegroundService refused; falling back to startService", e);
            startService(intent);
        }
    }

    /**
     * Android 13+ hides the VPN's persistent notification (the only
     * Disconnect control outside the app) unless POST_NOTIFICATIONS is
     * granted. Ask on the first Connect; the answer is not needed for the
     * VPN itself, so the result is ignored.
     */
    private void requestNotificationPermission() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) {
            return;
        }
        if (ContextCompat.checkSelfPermission(this, "android.permission.POST_NOTIFICATIONS")
                == PackageManager.PERMISSION_GRANTED) {
            return;
        }
        requestPermissions(new String[]{"android.permission.POST_NOTIFICATIONS"},
                NOTIFICATION_PERMISSION_REQUEST);
    }

    /**
     * Launch the system camera to capture a photo (video=false) or a video
     * (video=true). The capture is written to a FileProvider URI in the cache and
     * the result is delivered to JS as a "common:capture" event.
     */
    public void launchCameraCapture(boolean video) {
        // Context.checkSelfPermission is API 23; minSdk is 21.
        if (ContextCompat.checkSelfPermission(this, "android.permission.CAMERA")
                != PackageManager.PERMISSION_GRANTED) {
            pendingCaptureIsVideo = video;
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
                requestPermissions(new String[]{"android.permission.CAMERA"}, CAMERA_PERMISSION_REQUEST);
            } else {
                bridge.emitEvent("common:capture", "{\"error\":\"camera permission denied\"}");
            }
            return;
        }
        try {
            File dir = new File(getCacheDir(), "captures");
            if (!dir.exists()) dir.mkdirs();
            pendingCaptureFile = new File(dir, "capture_" + System.currentTimeMillis() + (video ? ".mp4" : ".jpg"));
            pendingCaptureIsVideo = video;
            Uri uri = FileProvider.getUriForFile(this, getPackageName() + ".fileprovider", pendingCaptureFile);
            Intent intent = new Intent(video ? MediaStore.ACTION_VIDEO_CAPTURE : MediaStore.ACTION_IMAGE_CAPTURE);
            intent.putExtra(MediaStore.EXTRA_OUTPUT, uri);
            intent.addFlags(Intent.FLAG_GRANT_WRITE_URI_PERMISSION);
            // Don't pre-check with resolveActivity(): Android 11+ package visibility
            // hides other apps' intents unless declared in <queries>, so it can
            // return null even when a camera app exists. Just launch and handle a miss.
            startActivityForResult(intent, video ? VIDEO_CAPTURE_REQUEST : PHOTO_CAPTURE_REQUEST);
        } catch (android.content.ActivityNotFoundException e) {
            bridge.emitEvent("common:capture", "{\"error\":\"no camera app available\"}");
        } catch (Exception e) {
            Log.e(TAG, "launchCameraCapture failed", e);
            bridge.emitEvent("common:capture", "{\"error\":\"capture failed\"}");
        }
    }

    @Override
    public void onRequestPermissionsResult(int requestCode, String[] permissions, int[] grantResults) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults);
        if (requestCode == NOTIFICATION_PERMISSION_REQUEST) {
            // Cosmetic only: the VPN runs (and is controllable in-app) with
            // the notification suppressed. Never forward to the bridge.
            return;
        }
        if (requestCode == CAMERA_PERMISSION_REQUEST) {
            if (grantResults.length > 0 && grantResults[0] == PackageManager.PERMISSION_GRANTED) {
                launchCameraCapture(pendingCaptureIsVideo);
            } else {
                bridge.emitEvent("common:capture", "{\"error\":\"camera permission denied\"}");
            }
            return;
        }
        if (bridge != null) {
            bridge.onRequestPermissionsResult(requestCode, grantResults);
        }
    }

    private void handleCaptureResult(int resultCode, @Nullable Intent data) {
        File file = pendingCaptureFile;
        final boolean video = pendingCaptureIsVideo;
        pendingCaptureFile = null;
        if (resultCode != RESULT_OK) {
            bridge.emitEvent("common:capture", "{\"cancelled\":true}");
            return;
        }
        // Some camera apps (commonly for video) ignore EXTRA_OUTPUT and instead
        // return a content URI in the result data; copy that into our cache.
        if ((file == null || !file.exists() || file.length() == 0)
                && data != null && data.getData() != null) {
            String copied = copyUriToCache(data.getData());
            if (copied != null) file = new File(copied);
        }
        final File f = file;
        if (f == null || !f.exists() || f.length() == 0) {
            bridge.emitEvent("common:capture", "{\"cancelled\":true}");
            return;
        }
        new Thread(() -> {
            try {
                JSONObject o = new JSONObject();
                o.put("type", video ? "video" : "photo");
                o.put("path", f.getAbsolutePath());
                o.put("size", f.length());
                if (!video) {
                    String thumb = makePhotoThumbnail(f);
                    if (thumb != null) o.put("thumb", thumb);
                }
                // Stream URL works for both: <video>/<img> load it from the cache
                // via shouldInterceptRequest (Range-enabled), no size limit.
                o.put("streamUrl", captureStreamUrl(f));
                bridge.emitEvent("common:capture", o.toString());
            } catch (Exception e) {
                Log.e(TAG, "handleCaptureResult failed", e);
                bridge.emitEvent("common:capture", "{\"error\":\"result processing failed\"}");
            }
        }).start();
    }

    /** Downscale a captured photo into a base64 JPEG data URL for display in the webview. */
    @Nullable
    private String makePhotoThumbnail(File file) {
        try {
            BitmapFactory.Options bounds = new BitmapFactory.Options();
            bounds.inJustDecodeBounds = true;
            BitmapFactory.decodeFile(file.getAbsolutePath(), bounds);
            int sample = 1;
            while (Math.max(bounds.outWidth, bounds.outHeight) / sample > 640) sample *= 2;
            BitmapFactory.Options opts = new BitmapFactory.Options();
            opts.inSampleSize = sample;
            Bitmap bmp = BitmapFactory.decodeFile(file.getAbsolutePath(), opts);
            if (bmp == null) return null;
            ByteArrayOutputStream baos = new ByteArrayOutputStream();
            bmp.compress(Bitmap.CompressFormat.JPEG, 70, baos);
            bmp.recycle();
            return "data:image/jpeg;base64," + Base64.encodeToString(baos.toByteArray(), Base64.NO_WRAP);
        } catch (Exception e) {
            return null;
        }
    }

    /**
     * Build a same-origin URL the webview can stream a capture from. Served by
     * serveCaptureFile (via shouldInterceptRequest); the path is relative to the
     * cache dir so both camera files (captures/) and copied content URIs
     * (wails-picker/) resolve.
     */
    private String captureStreamUrl(File file) {
        String base = getCacheDir().getAbsolutePath() + File.separator;
        String abs = file.getAbsolutePath();
        String rel = abs.startsWith(base) ? abs.substring(base.length()) : file.getName();
        return "/__capture__/" + Uri.encode(rel, "/");
    }

    /**
     * Serve a captured file (under the app cache) to the webview with HTTP Range
     * support, so &lt;video&gt; can stream and seek a clip of any length without
     * inlining it as a data URL.
     */
    private WebResourceResponse serveCaptureFile(String relPath, WebResourceRequest request) {
        try {
            File cache = getCacheDir();
            File file = new File(cache, Uri.decode(relPath));
            // Path-traversal guard: only ever serve files under the cache dir.
            if (!file.getCanonicalPath().startsWith(cache.getCanonicalPath() + File.separator)
                    || !file.exists() || !file.isFile()) {
                return new WebResourceResponse("text/plain", "UTF-8", 404, "Not Found",
                        new java.util.HashMap<>(), new java.io.ByteArrayInputStream(new byte[0]));
            }
            String name = file.getName().toLowerCase();
            String mime = name.endsWith(".mp4") ? "video/mp4"
                    : name.endsWith(".mov") ? "video/quicktime"
                    : name.endsWith(".jpg") || name.endsWith(".jpeg") ? "image/jpeg"
                    : name.endsWith(".png") ? "image/png" : "application/octet-stream";
            long length = file.length();
            java.util.Map<String, String> reqHeaders = request.getRequestHeaders();
            String range = reqHeaders != null ? reqHeaders.get("Range") : null;
            if (range == null && reqHeaders != null) range = reqHeaders.get("range");

            java.util.Map<String, String> headers = new java.util.HashMap<>();
            headers.put("Accept-Ranges", "bytes");
            headers.put("Cache-Control", "no-store");

            if (range != null && range.startsWith("bytes=")) {
                long start = 0, end = length - 1;
                String spec = range.substring(6).trim();
                int dash = spec.indexOf('-');
                if (dash >= 0) {
                    try {
                        if (dash > 0) start = Long.parseLong(spec.substring(0, dash).trim());
                        String e = spec.substring(dash + 1).trim();
                        if (!e.isEmpty()) end = Long.parseLong(e);
                    } catch (NumberFormatException ignored) { }
                }
                if (start < 0) start = 0;
                if (end >= length) end = length - 1;
                if (start > end) { start = 0; end = length - 1; }
                long count = end - start + 1;
                java.io.InputStream in = new java.io.FileInputStream(file);
                long toSkip = start;
                while (toSkip > 0) {
                    long s = in.skip(toSkip);
                    if (s <= 0) break;
                    toSkip -= s;
                }
                headers.put("Content-Range", "bytes " + start + "-" + end + "/" + length);
                headers.put("Content-Length", String.valueOf(count));
                return new WebResourceResponse(mime, null, 206, "Partial Content",
                        headers, new LimitedInputStream(in, count));
            }
            headers.put("Content-Length", String.valueOf(length));
            return new WebResourceResponse(mime, null, 200, "OK", headers,
                    new java.io.FileInputStream(file));
        } catch (Exception e) {
            Log.e(TAG, "serveCaptureFile failed", e);
            return new WebResourceResponse("text/plain", "UTF-8", 500, "Error",
                    new java.util.HashMap<>(), new java.io.ByteArrayInputStream(new byte[0]));
        }
    }

    /** Wraps a stream to yield at most a fixed number of bytes (for Range responses). */
    private static final class LimitedInputStream extends java.io.FilterInputStream {
        private long remaining;
        LimitedInputStream(java.io.InputStream in, long limit) {
            super(in);
            this.remaining = limit;
        }
        @Override public int read() throws java.io.IOException {
            if (remaining <= 0) return -1;
            int b = super.read();
            if (b >= 0) remaining--;
            return b;
        }
        @Override public int read(byte[] b, int off, int len) throws java.io.IOException {
            if (remaining <= 0) return -1;
            int n = super.read(b, off, (int) Math.min(len, remaining));
            if (n > 0) remaining -= n;
            return n;
        }
    }

    /**
     * Launch the system document picker. Results are copied into the app's
     * cache directory so Go receives real filesystem paths. Called by
     * WailsBridge on the main thread.
     */
    public void launchFilePicker(int callbackID, boolean multiple) {
        synchronized (this) {
            if (pendingFilePickerCallbackID != -1) {
                // Only one picker can be in flight
                bridge.filePickerDone(callbackID);
                return;
            }
            pendingFilePickerCallbackID = callbackID;
        }

        Intent intent = new Intent(Intent.ACTION_OPEN_DOCUMENT);
        intent.addCategory(Intent.CATEGORY_OPENABLE);
        intent.setType("*/*");
        intent.putExtra(Intent.EXTRA_ALLOW_MULTIPLE, multiple);
        try {
            startActivityForResult(intent, FILE_PICKER_REQUEST);
        } catch (Exception e) {
            Log.e(TAG, "Failed to launch file picker", e);
            pendingFilePickerCallbackID = -1;
            bridge.filePickerDone(callbackID);
        }
    }

    @Override
    protected void onActivityResult(int requestCode, int resultCode, @Nullable Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (requestCode == VPN_REQUEST_CODE) {
            // The system VPN dialog was answered. Report it to Go,
            // which records the state and emits "vpn:permission".
            // A denial is not terminal: Android shows the dialog
            // again on the next request; only the user revoking in
            // system Settings makes it permanent.
            try {
                nativeVpnPermissionResult(resultCode == RESULT_OK);
            } catch (UnsatisfiedLinkError e) {
                Log.w(TAG, "nativeVpnPermissionResult unavailable", e);
            }
            // A start that waited on this dialog goes ahead now that
            // permission is granted; a denial just clears the flag.
            if (pendingStartVpn) {
                if (resultCode == RESULT_OK) {
                    resumePendingStart();
                } else {
                    pendingStartVpn = false;
                }
            }
            return;
        }
        if (requestCode == PHOTO_CAPTURE_REQUEST || requestCode == VIDEO_CAPTURE_REQUEST) {
            handleCaptureResult(resultCode, data);
            return;
        }
        if (requestCode == WEBVIEW_FILE_CHOOSER_REQUEST) {
            if (webViewFileChooserCallback == null) {
                return;
            }
            Uri[] results = null;
            if (resultCode == RESULT_OK && data != null) {
                if (data.getClipData() != null) {
                    // Multiple selection (<input type="file" multiple>) is
                    // returned as ClipData. FileChooserParams.parseResult only
                    // reads getData(), so it would drop all but one (and return
                    // null when getData() is empty) — read every item explicitly.
                    int count = data.getClipData().getItemCount();
                    java.util.List<Uri> validated = new java.util.ArrayList<>(count);
                    for (int i = 0; i < count; i++) {
                        Uri uri = data.getClipData().getItemAt(i).getUri();
                        if (isFileChooserUriSafe(uri)) validated.add(uri);
                    }
                    if (!validated.isEmpty()) {
                        results = validated.toArray(new Uri[0]);
                    }
                } else if (data.getData() != null) {
                    Uri uri = data.getData();
                    if (isFileChooserUriSafe(uri)) {
                        results = new Uri[]{ uri };
                    }
                }
            }
            if (results == null) {
                // Fallback for pickers that report the result differently.
                results = FileChooserParams.parseResult(resultCode, data);
            }
            webViewFileChooserCallback.onReceiveValue(results);
            webViewFileChooserCallback = null;
            return;
        }
        if (requestCode != FILE_PICKER_REQUEST) {
            return;
        }
        final int callbackID = pendingFilePickerCallbackID;
        pendingFilePickerCallbackID = -1;
        if (callbackID == -1) {
            return;
        }

        final List<Uri> uris = new ArrayList<>();
        if (resultCode == RESULT_OK && data != null) {
            if (data.getClipData() != null) {
                for (int i = 0; i < data.getClipData().getItemCount(); i++) {
                    uris.add(data.getClipData().getItemAt(i).getUri());
                }
            } else if (data.getData() != null) {
                uris.add(data.getData());
            }
        }

        // Copy the documents off the main thread, then notify Go
        new Thread(() -> {
            for (Uri uri : uris) {
                String path = copyUriToCache(uri);
                if (path != null) {
                    bridge.filePickerResult(callbackID, path);
                }
            }
            bridge.filePickerDone(callbackID);
        }).start();
    }

    /**
     * Validate that a file-chooser URI is safe to pass to the WebView.
     * Rejects file:// URIs pointing at the app's own private data (a malicious
     * third-party picker could return such URIs to exfiltrate sensitive files).
     * Only content:// URIs (the standard document-picker output) are accepted.
     */
    private boolean isFileChooserUriSafe(Uri uri) {
        if (uri == null) return false;
        String scheme = uri.getScheme();
        // content:// URIs are the standard, safe output of document pickers.
        if ("content".equals(scheme)) return true;
        // Reject file:// URIs — they can point at app-private data.
        // Also reject anything else (null scheme, unknown schemes).
        if (DEBUG) Log.w(TAG, "Rejected unsafe file-chooser URI: " + uri);
        return false;
    }

    /**
     * Copy a content URI into the app cache and return its filesystem path.
     */
    @Nullable
    private String copyUriToCache(Uri uri) {
        String name = "document";
        try (Cursor cursor = getContentResolver().query(uri, null, null, null, null)) {
            if (cursor != null && cursor.moveToFirst()) {
                int idx = cursor.getColumnIndex(OpenableColumns.DISPLAY_NAME);
                if (idx >= 0 && cursor.getString(idx) != null) {
                    name = new File(cursor.getString(idx)).getName();
                }
            }
        } catch (Exception ignored) {
        }

        try {
            File dir = new File(getCacheDir(), "wails-picker/" + System.nanoTime());
            if (!dir.mkdirs()) {
                return null;
            }
            File out = new File(dir, name);
            try (InputStream in = getContentResolver().openInputStream(uri);
                 OutputStream os = new FileOutputStream(out)) {
                if (in == null) {
                    return null;
                }
                byte[] buf = new byte[64 * 1024];
                int n;
                while ((n = in.read(buf)) > 0) {
                    os.write(buf, 0, n);
                }
            }
            return out.getAbsolutePath();
        } catch (Exception e) {
            Log.e(TAG, "Failed to copy picked document", e);
            return null;
        }
    }

    /**
     * Execute JavaScript in the WebView from the Go side
     */
    public void executeJavaScript(final String js) {
        runOnUiThread(() -> {
            if (webView != null) {
                webView.evaluateJavascript(js, null);
            }
        });
    }

    // ---- System events ---------------------------------------------------
    // Battery/power, screen lock and network connectivity are surfaced to JS as
    // "system:*" events. The OS broadcasts used here (ACTION_BATTERY_CHANGED,
    // SCREEN_OFF, USER_PRESENT, POWER_SAVE_MODE_CHANGED) are protected system
    // broadcasts, so dynamic registration needs no RECEIVER_* export flag.

    private void registerSystemEventReceivers() {
        // Battery + charging state (sticky broadcast: the current value is
        // delivered to the receiver immediately on registration).
        batteryReceiver = new BroadcastReceiver() {
            @Override public void onReceive(Context context, Intent intent) {
                emitBattery(intent);
            }
        };
        registerReceiver(batteryReceiver, new IntentFilter(Intent.ACTION_BATTERY_CHANGED));

        // Low-power (battery saver) mode toggles → re-emit battery with the flag.
        powerSaveReceiver = new BroadcastReceiver() {
            @Override public void onReceive(Context context, Intent intent) {
                emitBattery(registerSticky(Intent.ACTION_BATTERY_CHANGED));
            }
        };
        registerReceiver(powerSaveReceiver,
                new IntentFilter(PowerManager.ACTION_POWER_SAVE_MODE_CHANGED));

        // Screen lock / unlock. SCREEN_OFF ≈ locked; USER_PRESENT = unlocked.
        screenReceiver = new BroadcastReceiver() {
            @Override public void onReceive(Context context, Intent intent) {
                String action = intent.getAction();
                if (Intent.ACTION_SCREEN_OFF.equals(action)) {
                    emitLock(true);
                } else if (Intent.ACTION_USER_PRESENT.equals(action)) {
                    emitLock(false);
                }
            }
        };
        IntentFilter screenFilter = new IntentFilter();
        screenFilter.addAction(Intent.ACTION_SCREEN_OFF);
        screenFilter.addAction(Intent.ACTION_USER_PRESENT);
        registerReceiver(screenReceiver, screenFilter);

        // Network connectivity / transport type / cellular signal strength.
        connectivityManager = (ConnectivityManager) getSystemService(Context.CONNECTIVITY_SERVICE);
        if (connectivityManager != null) {
            networkCallback = new ConnectivityManager.NetworkCallback() {
                @Override public void onAvailable(Network network) { emitNetwork(network); }
                @Override public void onLost(Network network) { emitNetworkDisconnected(); }
                @Override public void onCapabilitiesChanged(Network network, NetworkCapabilities caps) {
                    emitNetwork(network);
                }
            };
            try {
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N) {
                    connectivityManager.registerDefaultNetworkCallback(networkCallback);
                } else {
                    // registerDefaultNetworkCallback is API 24; minSdk is 21.
                    // The request form reports the same networks (including
                    // the current one immediately on registration).
                    connectivityManager.registerNetworkCallback(
                            new NetworkRequest.Builder()
                                    .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
                                    .build(),
                            networkCallback);
                }
            } catch (Exception e) {
                Log.e(TAG, "registerDefaultNetworkCallback failed", e);
            }
        }
    }

    private void unregisterSystemEventReceivers() {
        safeUnregister(batteryReceiver);
        batteryReceiver = null;
        safeUnregister(powerSaveReceiver);
        powerSaveReceiver = null;
        safeUnregister(screenReceiver);
        screenReceiver = null;
        if (connectivityManager != null && networkCallback != null) {
            try {
                connectivityManager.unregisterNetworkCallback(networkCallback);
            } catch (Exception ignored) {
            }
            networkCallback = null;
        }
    }

    private void safeUnregister(BroadcastReceiver r) {
        if (r != null) {
            try {
                unregisterReceiver(r);
            } catch (Exception ignored) {
            }
        }
    }

    /** Read the current sticky value for an action without a standing receiver. */
    @Nullable
    private Intent registerSticky(String action) {
        return registerReceiver(null, new IntentFilter(action));
    }

    /** Push current battery / network / theme so a freshly-loaded UI is populated. */
    private void emitSystemSnapshot() {
        emitBattery(registerSticky(Intent.ACTION_BATTERY_CHANGED));
        // ConnectivityManager.getActiveNetwork is API 23; below that the
        // network callback registered in onStart has already reported the
        // current network, so skipping here loses nothing.
        if (connectivityManager != null && Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
            Network active = connectivityManager.getActiveNetwork();
            if (active != null) {
                emitNetwork(active);
            } else {
                emitNetworkDisconnected();
            }
        }
        emitTheme();
    }

    private void emitBattery(@Nullable Intent batteryStatus) {
        try {
            float level = -1f;
            String state = "unknown";
            if (batteryStatus != null) {
                int lvl = batteryStatus.getIntExtra(BatteryManager.EXTRA_LEVEL, -1);
                int scale = batteryStatus.getIntExtra(BatteryManager.EXTRA_SCALE, -1);
                if (lvl >= 0 && scale > 0) {
                    level = lvl / (float) scale;
                }
                switch (batteryStatus.getIntExtra(BatteryManager.EXTRA_STATUS, -1)) {
                    case BatteryManager.BATTERY_STATUS_CHARGING: state = "charging"; break;
                    case BatteryManager.BATTERY_STATUS_FULL: state = "full"; break;
                    case BatteryManager.BATTERY_STATUS_DISCHARGING:
                    case BatteryManager.BATTERY_STATUS_NOT_CHARGING: state = "unplugged"; break;
                    default: state = "unknown"; break;
                }
            }
            boolean lowPower = false;
            PowerManager pm = (PowerManager) getSystemService(Context.POWER_SERVICE);
            if (pm != null) {
                lowPower = pm.isPowerSaveMode();
            }
            JSONObject o = new JSONObject();
            o.put("level", (double) level);
            o.put("state", state);
            o.put("lowPowerMode", lowPower);
            if (bridge != null) bridge.emitSystemEvent("android:BatteryChanged", o.toString());
        } catch (Exception e) {
            Log.e(TAG, "emitBattery failed", e);
        }
    }

    private void emitNetwork(@Nullable Network network) {
        try {
            boolean connected = false;
            String type = "none";
            boolean metered = false;
            Integer signal = null;
            if (connectivityManager != null && network != null) {
                NetworkCapabilities caps = connectivityManager.getNetworkCapabilities(network);
                if (caps != null) {
                    connected = caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET);
                    if (caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)) {
                        type = "wifi";
                    } else if (caps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR)) {
                        type = "cellular";
                    } else if (caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET)) {
                        type = "wired";
                    } else {
                        type = "other";
                    }
                    metered = !caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED);
                    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                        int s = caps.getSignalStrength();
                        if (s != Integer.MIN_VALUE) {
                            signal = s; // dBm; closer to 0 is a stronger signal
                        }
                    }
                }
            }
            JSONObject o = new JSONObject();
            o.put("connected", connected);
            o.put("type", type);
            o.put("metered", metered);
            if (signal != null) {
                o.put("signal", (int) signal);
            }
            if (bridge != null) bridge.emitSystemEvent("android:NetworkChanged", o.toString());
        } catch (Exception e) {
            Log.e(TAG, "emitNetwork failed", e);
        }
    }

    private void emitNetworkDisconnected() {
        try {
            JSONObject o = new JSONObject();
            o.put("connected", false);
            o.put("type", "none");
            o.put("metered", false);
            if (bridge != null) bridge.emitSystemEvent("android:NetworkChanged", o.toString());
        } catch (Exception ignored) {
        }
    }

    private void emitLock(boolean locked) {
        // Lock/unlock are signals (no payload); name carries the state.
        if (bridge != null) {
            bridge.emitSystemEvent(locked ? "android:ScreenLocked" : "android:ScreenUnlocked", "{}");
        }
    }

    private void emitTheme() {
        try {
            int mode = getResources().getConfiguration().uiMode & Configuration.UI_MODE_NIGHT_MASK;
            JSONObject o = new JSONObject();
            // "isDarkMode" matches the context key the desktop platforms use.
            o.put("isDarkMode", mode == Configuration.UI_MODE_NIGHT_YES);
            if (bridge != null) bridge.emitSystemEvent("android:ThemeChanged", o.toString());
        } catch (Exception ignored) {
        }
    }

    @Override
    public void onConfigurationChanged(Configuration newConfig) {
        super.onConfigurationChanged(newConfig);
        // Fires for light/dark switches because the manifest lists uiMode in
        // android:configChanges (otherwise the activity would be recreated).
        emitTheme();
    }

    @Override
    protected void onStart() {
        super.onStart();
        // Battery: only monitor system events while the app is visible.
        if (!systemReceiversRegistered) {
            registerSystemEventReceivers();
            systemReceiversRegistered = true;
        }
        if (bridge != null) {
            bridge.onStart();
        }
    }

    @Override
    protected void onResume() {
        super.onResume();
        if (bridge != null) {
            bridge.onResume();
        }
    }

    @Override
    protected void onPause() {
        super.onPause();
        if (bridge != null) {
            bridge.onPause();
        }
    }

    @Override
    protected void onStop() {
        super.onStop();
        if (systemReceiversRegistered) {
            unregisterSystemEventReceivers();
            systemReceiversRegistered = false;
        }
        if (bridge != null) {
            bridge.onStop();
        }
    }

    @Override
    public void onLowMemory() {
        super.onLowMemory();
        if (bridge != null) {
            bridge.onLowMemory();
        }
    }

    @Override
    protected void onDestroy() {
        super.onDestroy();
        unregisterSystemEventReceivers();
        if (bridge != null) {
            bridge.shutdown();
        }
        if (webView != null) {
            webView.destroy();
            // The back probe and pending JS callbacks must not touch a
            // destroyed WebView.
            webView = null;
        }
    }
}
