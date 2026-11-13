package com.skein.app;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.content.Context;
import android.content.Intent;
import android.content.pm.ServiceInfo;
import android.net.VpnService;
import android.net.wifi.WifiManager;
import android.os.Build;
import android.os.ParcelFileDescriptor;
import android.util.Log;

import androidx.core.app.NotificationCompat;

import org.json.JSONObject;

import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.RejectedExecutionException;
import java.util.concurrent.TimeUnit;

/**
 * SkeinVpnService: the Android VPN endpoint for the Skein mesh.
 *
 * <p>Flow (driven by the Java side; the Go daemon runs in-process in
 * libwails.so, started by the Wails runtime before this service can
 * run):
 *
 * <ol>
 *   <li>MainActivity calls {@link #prepareIntent} and, once the user
 *       grants VPN permission, starts this service with
 *       {@link #connectIntent}.</li>
 *   <li>{@code connect()} starts the Go daemon
 *       ({@code nativeStartDaemon}), reads the overlay config
 *       ({@code nativeGetVpnConfig}), builds the TUN with
 *       {@link VpnService.Builder}, and passes the FD to Go
 *       ({@code nativeSetTunFd}).</li>
 *   <li>Go connects the dataplane ({@code nativeConnectDaemon}); on
 *       success the service stays foreground with a persistent
 *       notification until the user disconnects or the permission is
 *       revoked. On failure everything is torn down and a one-shot
 *       notification carries the reason: no half-connected state.</li>
 * </ol>
 *
 * <p>Threading: every daemon/TUN transition runs on one worker
 * thread, in submission order. {@code daemon.Connect/Stop} are not
 * reentrant, and a restart is {@code Disconnect} followed by
 * {@code Connect}, so letting the two run concurrently left the
 * freshly started daemon killed by the teardown that was still in
 * flight (the app then showed a tunnel that was already gone). The
 * worker also makes teardown idempotent: it never runs twice at once.
 *
 * <p>Routing: only the overlay prefixes are added as routes, so
 * underlay traffic (the daemon's UDP to peers' public addresses)
 * is not routed into the TUN by the route table — but the daemon's
 * socket is still bound to the VPN network, so its packets are
 * captured unless the socket is protected. The Go daemon protects
 * its underlay UDP socket via {@link #protectSocket} (registered
 * from the GUI at startup) as soon as it is created; without that
 * the peer is never reached and never observed. {@code allowBypass()}
 * is set so other apps may bypass the VPN via
 * {@code ConnectivityManager.bindProcessToNetwork}; it does not
 * protect the daemon's own sockets (that is {@code protect()}).
 * If a full-tunnel (0.0.0.0/0) route is ever added, the underlay
 * socket MUST be protected first — it already is.
 *
 * <p>FD ownership: {@code establish()} returns a
 * {@link ParcelFileDescriptor}; {@code detachFd()} transfers
 * ownership of the raw FD to Go, and closing the
 * ParcelFileDescriptor afterwards does not close it. Go closes the
 * FD when the daemon's TUN device closes.
 */
public class SkeinVpnService extends VpnService {
    private static final String TAG = "SkeinVpnService";

    public static final String ACTION_CONNECT = "com.skein.app.CONNECT";
    public static final String ACTION_DISCONNECT = "com.skein.app.DISCONNECT";

    private static final String CHANNEL_ID = "skein-vpn";
    private static final int NOTIFICATION_ID = 1;
    private static final int FAILURE_NOTIFICATION_ID = 2;

    /** Serializes every daemon/TUN transition (see the class doc). */
    private ExecutorService worker;

    // Worker-thread state.
    private ParcelFileDescriptor tunFd;
    private boolean daemonRunning;
    private boolean connected;
    private String connectedText = "Connected to the mesh";
    private WifiManager.MulticastLock multicastLock;
    private volatile WifiManager.WifiLock highPerfLock;

    // App-private preference: hold a high-performance Wi-Fi lock while the
    // tunnel is up. Off by default — it keeps the radio awake and costs
    // battery, which only pays off when the user wants the lowest possible
    // latency on a LAN.
    private static final String PREFS = "skein";
    private static final String PREF_HIGH_PERF_WIFI = "high_perf_wifi";

    // Most recent start request, written by onStartCommand on the main
    // thread and read by the worker: teardown only stops the service when
    // the latest request was a Disconnect, so a Connect queued behind it
    // (ServiceService.Restart) keeps the service alive.
    private volatile String lastAction;
    private volatile boolean destroyed;

    // The running service instance, for protectSocket: Go's underlay
    // UDP socket must be protected from the VPN, and Go calls this
    // static method with the socket's FD (see gui/jni_android.go).
    private static SkeinVpnService instance;

    /**
     * Protect a socket FD from the VPN so its traffic bypasses the
     * TUN. The daemon's underlay UDP socket needs this: without it
     * the socket's packets to a peer's LAN/public address are routed
     * into the TUN instead of out on the real network, so the peer
     * is never reached (and never observed). Returns false when no
     * service is running.
     */
    public static boolean protectSocket(int fd) {
        SkeinVpnService svc = instance;
        if (svc == null) {
            Log.w(TAG, "protectSocket fd=" + fd + ": no service instance yet");
            return false;
        }
        boolean ok = svc.protect(fd);
        Log.i(TAG, "protectSocket fd=" + fd + ": " + ok);
        return ok;
    }

    /**
     * Persist the high-performance Wi-Fi preference and apply it to a running
     * tunnel. Called from MainActivity (Settings → Connection).
     */
    public static void setHighPerformance(Context context, boolean enabled) {
        context.getApplicationContext()
                .getSharedPreferences(PREFS, Context.MODE_PRIVATE)
                .edit()
                .putBoolean(PREF_HIGH_PERF_WIFI, enabled)
                .apply();
        SkeinVpnService svc = instance;
        if (svc != null) {
            svc.applyHighPerformance();
        }
        Log.i(TAG, "high-performance Wi-Fi " + (enabled ? "enabled" : "disabled"));
    }

    /** Whether the user asked for the high-performance Wi-Fi lock. */
    public static boolean highPerformanceEnabled(Context context) {
        return context.getApplicationContext()
                .getSharedPreferences(PREFS, Context.MODE_PRIVATE)
                .getBoolean(PREF_HIGH_PERF_WIFI, false);
    }

    // JNI: implemented in Go (gui/jni_android.go, in libwails.so,
    // already loaded by WailsBridge). Status: 0 ok, negative error.
    private native int nativeSetTunFd(int fd);
    private native int nativeStartDaemon(String filesDir);
    private native int nativeStopDaemon();
    private native int nativeConnectDaemon();
    private native int nativeDisconnectDaemon();
    private native String nativeGetVpnConfig();
    // Reports a failed start/connect to Go (services.NoteVpnError) so the
    // Wails service call that started us can return the reason to the UI.
    // Static so MainActivity can report a start it could not hand over.
    private static native void nativeVpnFailure(String reason);

    /** Report a failed start to Go. Never throws. */
    static void reportFailure(String reason) {
        try {
            nativeVpnFailure(reason);
        } catch (Throwable t) {
            Log.w(TAG, "reporting the failure to Go failed", t);
        }
    }

    /** Intent that starts the VPN. */
    public static Intent connectIntent(Context context) {
        return new Intent(context, SkeinVpnService.class).setAction(ACTION_CONNECT);
    }

    /** Intent that stops the VPN. */
    public static Intent disconnectIntent(Context context) {
        return new Intent(context, SkeinVpnService.class).setAction(ACTION_DISCONNECT);
    }

    /**
     * Returns the system VPN-consent intent, or null when Skein is
     * already authorised. Must be launched from an Activity (the
     * Connect button flow); only after the user accepts it may the
     * service be started.
     */
    public static Intent prepareIntent(Context context) {
        return VpnService.prepare(context);
    }

    @Override
    public void onCreate() {
        super.onCreate();
        worker = Executors.newSingleThreadExecutor(r -> {
            Thread t = new Thread(r, "skein-vpn");
            t.setDaemon(true);
            return t;
        });
        instance = this;
        createNotificationChannel();
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        if (intent == null) {
            return START_NOT_STICKY;
        }
        String action = intent.getAction();
        if (ACTION_DISCONNECT.equals(action)) {
            lastAction = ACTION_DISCONNECT;
            // The UI starts this with startForegroundService (it may be
            // backgrounded when Disconnect is tapped): the service must
            // enter the foreground within 5s even on the way out, or the
            // system kills it mid-teardown.
            if (!enterForeground(buildNotification("Skein VPN", "Disconnecting…"))) {
                stopSelfResult(startId);
                return START_NOT_STICKY;
            }
            submit(() -> teardownNow(!ACTION_CONNECT.equals(lastAction)));
            return START_NOT_STICKY;
        }
        if (ACTION_CONNECT.equals(action)) {
            lastAction = ACTION_CONNECT;
            if (!enterForeground(buildNotification("Skein VPN — Connecting", "Starting the mesh…"))) {
                stopSelfResult(startId);
                return START_NOT_STICKY;
            }
            submit(this::connect);
            return START_NOT_STICKY;
        }
        return START_NOT_STICKY;
    }

    @Override
    public void onRevoke() {
        // The user revoked VPN permission in system settings (or
        // another VPN took over). Tear down; do not fight the system.
        Log.i(TAG, "VPN permission revoked, tearing down");
        submit(() -> teardownNow(true));
        super.onRevoke();
    }

    @Override
    public void onDestroy() {
        destroyed = true;
        // The system can destroy this service without a Disconnect
        // (stopService from elsewhere, a foreground-service timeout, task
        // removal). Never leave the daemon and its TUN behind: finish the
        // teardown on the worker, bounded so the main thread is not held.
        ExecutorService w = worker;
        worker = null;
        if (w != null) {
            try {
                Future<?> done = w.submit(() -> teardownNow(false));
                done.get(2, TimeUnit.SECONDS);
            } catch (Exception e) {
                Log.w(TAG, "teardown on destroy did not finish; it continues on the worker", e);
            }
            // shutdown() lets an already-queued teardown run to completion.
            w.shutdown();
        }
        closeTun();
        releaseMulticastLock();
        if (instance == this) {
            instance = null;
        }
        super.onDestroy();
    }

    private void submit(Runnable task) {
        ExecutorService w = worker;
        if (w == null || destroyed) {
            return;
        }
        try {
            w.execute(task);
        } catch (RejectedExecutionException e) {
            Log.w(TAG, "service is going away; dropping request", e);
        }
    }

    /** Worker only: start the daemon, build the TUN, connect the dataplane. */
    private void connect() {
        if (destroyed) {
            return;
        }
        if (connected) {
            // A repeated Connect (double tap, a reconciler, a restart that
            // re-sent Connect) must not restart anything: nativeStartDaemon
            // refuses an already-running daemon and the old code then
            // stopped the service, killing a working tunnel.
            updateNotification("Skein VPN — Connected", connectedText);
            return;
        }
        if (!daemonRunning) {
            if (nativeStartDaemon(getFilesDir().getAbsolutePath()) != 0) {
                failAndTearDown("The Skein daemon did not start (see logcat: skein: android).");
                return;
            }
            daemonRunning = true;
        }
        if (!establishTun()) {
            return;
        }
        // The daemon browses mDNS the moment the dataplane starts; Android's
        // WiFi filter drops inbound multicast unless a MulticastLock is held.
        acquireMulticastLock();
        if (nativeConnectDaemon() != 0) {
            failAndTearDown("The mesh did not connect (see logcat: skein: android).");
            return;
        }
        connected = true;
        applyHighPerformance();
        updateNotification("Skein VPN — Connected", connectedText);
    }

    /**
     * Worker only: build the TUN from the daemon's overlay config and hand
     * its FD to Go. Returns false after tearing down: every failure here
     * leaves the user with no VPN rather than a half-built one.
     */
    private boolean establishTun() {
        String configJson = nativeGetVpnConfig();
        if (configJson == null) {
            failAndTearDown("The daemon has no network configuration yet. Finish onboarding first.");
            return false;
        }
        try {
            JSONObject cfg = new JSONObject(configJson);
            Builder builder = new Builder();
            builder.setSession("Skein");
            builder.addAddress(cfg.getString("overlay_ipv4"), cfg.getInt("overlay_ipv4_prefix"));
            builder.addRoute(cfg.getString("overlay_ipv4_network"), cfg.getInt("overlay_ipv4_prefix"));
            if (cfg.has("overlay_ipv6") && !cfg.isNull("overlay_ipv6")) {
                builder.addAddress(cfg.getString("overlay_ipv6"), cfg.getInt("overlay_ipv6_prefix"));
                builder.addRoute(cfg.getString("overlay_ipv6_network"), cfg.getInt("overlay_ipv6_prefix"));
            }
            builder.setMtu(cfg.getInt("mtu"));
            // See the class doc: allowBypass is for other apps, not
            // for the daemon's sockets; the overlay-only routes above
            // are what keep underlay traffic out of the TUN.
            builder.allowBypass();

            ParcelFileDescriptor pfd = builder.establish();
            if (pfd == null) {
                failAndTearDown("Android refused to create the tunnel (VPN permission revoked, or another VPN is active).");
                return false;
            }
            int fd = pfd.detachFd();
            if (nativeSetTunFd(fd) != 0) {
                try {
                    ParcelFileDescriptor.adoptFd(fd).close();
                } catch (Exception ignored) {
                    // Best effort: the FD leaks only on this error path.
                }
                failAndTearDown("The daemon rejected the tunnel descriptor.");
                return false;
            }
            // Go now owns the FD; the daemon's TUN Close is what
            // eventually closes it. Keeping the detached descriptor lets a
            // later teardown drop our reference without closing Go's FD.
            tunFd = pfd;
            String name = cfg.optString("name", "");
            connectedText = name.isEmpty() ? "Connected to the mesh" : "Connected to " + name;
            return true;
        } catch (Exception e) {
            Log.e(TAG, "building the TUN failed", e);
            failAndTearDown("Could not configure the tunnel: " + e.getMessage());
            return false;
        }
    }

    /**
     * Worker only: tear down the dataplane, the daemon and the TUN, then
     * stop the service when {@code stopService} is set. Safe to call when
     * nothing is running (onDestroy, a Disconnect for a dead tunnel).
     */
    private void teardownNow(boolean stopService) {
        connected = false;
        releaseMulticastLock();
        releaseHighPerfLock();
        closeTun();
        if (daemonRunning) {
            daemonRunning = false;
            try {
                nativeDisconnectDaemon();
            } catch (Throwable t) {
                Log.w(TAG, "nativeDisconnectDaemon failed", t);
            }
            try {
                nativeStopDaemon();
            } catch (Throwable t) {
                Log.w(TAG, "nativeStopDaemon failed", t);
            }
        }
        stopForegroundCompat();
        if (stopService) {
            stopSelf();
        }
    }

    /**
     * Worker only: fail closed and surface the reason. The in-app UI may be
     * backgrounded, so a failed connect also posts a one-shot notification;
     * without it a failed Connect tap looks like nothing happened.
     */
    private void failAndTearDown(String reason) {
        Log.e(TAG, reason);
        teardownNow(true);
        // Give the Wails service call that started us the reason: it waits
        // for the connection and returns this to the UI on timeout.
        try {
            nativeVpnFailure(reason);
        } catch (Throwable t) {
            Log.w(TAG, "reporting the failure to Go failed", t);
        }
        postFailure(reason);
    }

    /** Drops our (detached) ParcelFileDescriptor; the raw FD belongs to Go. */
    private void closeTun() {
        if (tunFd != null) {
            try {
                tunFd.close();
            } catch (Exception ignored) {
            }
            tunFd = null;
        }
    }

    /**
     * Hold the high-performance Wi-Fi lock only while the tunnel is up and the
     * user asked for it. Android's Wi-Fi power save parks the radio between
     * beacons, which delays inbound packets by up to ~100 ms; the lock keeps
     * the radio awake at the cost of battery.
     */
    private void applyHighPerformance() {
        if (connected && highPerformanceEnabled(this)) {
            acquireHighPerfLock();
        } else {
            releaseHighPerfLock();
        }
    }

    private void acquireHighPerfLock() {
        if (highPerfLock != null) {
            return;
        }
        try {
            WifiManager wifi =
                    (WifiManager) getApplicationContext().getSystemService(Context.WIFI_SERVICE);
            if (wifi == null) {
                return;
            }
            // LOW_LATENCY is the API 29+ mode for a foreground service; older
            // releases only have HIGH_PERF, which is deprecated but works.
            int mode = Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q
                    ? WifiManager.WIFI_MODE_FULL_LOW_LATENCY
                    : WifiManager.WIFI_MODE_FULL_HIGH_PERF;
            WifiManager.WifiLock lock = wifi.createWifiLock(mode, "skein-latency");
            lock.setReferenceCounted(false);
            lock.acquire();
            highPerfLock = lock;
            Log.i(TAG, "high-performance Wi-Fi lock acquired");
        } catch (Exception e) {
            Log.w(TAG, "high-performance Wi-Fi lock unavailable", e);
        }
    }

    private void releaseHighPerfLock() {
        WifiManager.WifiLock lock = highPerfLock;
        highPerfLock = null;
        if (lock == null) {
            return;
        }
        try {
            if (lock.isHeld()) {
                lock.release();
            }
        } catch (Exception e) {
            Log.w(TAG, "releasing the high-performance Wi-Fi lock failed", e);
        }
    }

    /**
     * Hold a WiFi multicast lock while the VPN is up: Android's WiFi filter
     * drops inbound multicast/broadcast, so mDNS browse silently sees no
     * peers even though the joins succeed. Reference-counted off so one
     * release always frees it.
     */
    private void acquireMulticastLock() {
        if (multicastLock != null) {
            return;
        }
        try {
            WifiManager wifi =
                    (WifiManager) getApplicationContext().getSystemService(Context.WIFI_SERVICE);
            if (wifi == null) {
                return;
            }
            WifiManager.MulticastLock lock = wifi.createMulticastLock("skein-mdns");
            lock.setReferenceCounted(false);
            lock.acquire();
            multicastLock = lock;
            Log.i(TAG, "WiFi multicast lock acquired");
        } catch (Exception e) {
            Log.w(TAG, "WiFi multicast lock unavailable", e);
        }
    }

    private void releaseMulticastLock() {
        WifiManager.MulticastLock lock = multicastLock;
        multicastLock = null;
        if (lock == null) {
            return;
        }
        try {
            if (lock.isHeld()) {
                lock.release();
            }
        } catch (Exception e) {
            Log.w(TAG, "releasing the WiFi multicast lock failed", e);
        }
    }

    private void createNotificationChannel() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) {
            return;
        }
        NotificationChannel channel = new NotificationChannel(
                CHANNEL_ID, "Skein VPN", NotificationManager.IMPORTANCE_LOW);
        channel.setDescription("Shows while the Skein mesh VPN is active");
        NotificationManager manager = notificationManager();
        if (manager != null) {
            manager.createNotificationChannel(channel);
        }
    }

    /** getSystemService(Class) is API 23; minSdk is 21. */
    private NotificationManager notificationManager() {
        return (NotificationManager) getSystemService(Context.NOTIFICATION_SERVICE);
    }

    private PendingIntent activityPendingIntent() {
        Intent activityIntent = new Intent(this, MainActivity.class);
        activityIntent.setFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP | Intent.FLAG_ACTIVITY_CLEAR_TOP);
        return PendingIntent.getActivity(
                this, 0, activityIntent,
                PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
    }

    private Notification buildNotification(String title, String text) {
        Intent disconnectIntent = new Intent(this, SkeinVpnService.class)
                .setAction(ACTION_DISCONNECT);
        PendingIntent disconnect = PendingIntent.getService(
                this, 0, disconnectIntent,
                PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);

        return new NotificationCompat.Builder(this, CHANNEL_ID)
                .setSmallIcon(R.mipmap.ic_launcher)
                .setContentTitle(title)
                .setContentText(text)
                .setContentIntent(activityPendingIntent())
                .addAction(new NotificationCompat.Action.Builder(
                        android.R.drawable.ic_menu_close_clear_cancel, "Disconnect", disconnect).build())
                .setOngoing(true)
                .build();
    }

    /**
     * Enter the foreground with the given notification. Returns false when
     * the system refused (a missing foregroundServiceType permission, or a
     * start from the background): continuing would be killed with a
     * ForegroundServiceDidNotStartInTimeException.
     */
    private boolean enterForeground(Notification notification) {
        try {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
                startForeground(NOTIFICATION_ID, notification,
                        ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE);
            } else {
                startForeground(NOTIFICATION_ID, notification);
            }
            return true;
        } catch (RuntimeException e) {
            Log.e(TAG, "startForeground failed", e);
            return false;
        }
    }

    private void updateNotification(String title, String text) {
        NotificationManager manager = notificationManager();
        if (manager != null) {
            manager.notify(NOTIFICATION_ID, buildNotification(title, text));
        }
    }

    /** One-shot notification for a failed connect (auto-cancels on tap). */
    private void postFailure(String reason) {
        try {
            NotificationManager manager = notificationManager();
            if (manager == null) {
                return;
            }
            manager.notify(FAILURE_NOTIFICATION_ID, new NotificationCompat.Builder(this, CHANNEL_ID)
                    .setSmallIcon(R.mipmap.ic_launcher)
                    .setContentTitle("Skein could not connect")
                    .setContentText(reason)
                    .setStyle(new NotificationCompat.BigTextStyle().bigText(reason))
                    .setContentIntent(activityPendingIntent())
                    .setAutoCancel(true)
                    .build());
        } catch (Exception e) {
            Log.w(TAG, "posting the failure notification failed", e);
        }
    }

    private void stopForegroundCompat() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N) {
            stopForeground(STOP_FOREGROUND_REMOVE);
        } else {
            stopForeground(true);
        }
    }
}
