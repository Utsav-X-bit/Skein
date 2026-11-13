package com.skein.app;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.content.Context;
import android.content.Intent;
import android.content.pm.ServiceInfo;
import android.net.VpnService;
import android.os.Build;
import android.os.ParcelFileDescriptor;
import android.util.Log;

import androidx.core.app.NotificationCompat;

import org.json.JSONObject;

/**
 * SkeinVpnService: the Android VPN endpoint for the Skein mesh.
 *
 * <p>Flow (driven by the Java side; the Go daemon runs in-process in
 * libwails.so, started by the Wails runtime before this service can
 * run):
 *
 * <ol>
 *   <li>MainActivity (Phase 3) calls {@link #prepareIntent} and, once
 *       the user grants VPN permission, starts this service with
 *       {@link #connectIntent}.</li>
 *   <li>{@code connect()} starts the Go daemon
 *       ({@code nativeStartDaemon}), reads the overlay config
 *       ({@code nativeGetVpnConfig}), builds the TUN with
 *       {@link VpnService.Builder}, and passes the FD to Go
 *       ({@code nativeSetTunFd}).</li>
 *   <li>Go connects the dataplane ({@code nativeConnectDaemon}); the
 *       service stays foreground with a persistent notification until
 *       the user disconnects or the permission is revoked.</li>
 * </ol>
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

    private ParcelFileDescriptor tunFd;
    private Thread connectThread;
    private volatile boolean connected;

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

    // JNI: implemented in Go (gui/jni_android.go, in libwails.so,
    // already loaded by WailsBridge). Status: 0 ok, negative error.
    private native int nativeSetTunFd(int fd);
    private native int nativeStartDaemon(String filesDir);
    private native int nativeStopDaemon();
    private native int nativeConnectDaemon();
    private native int nativeDisconnectDaemon();
    private native String nativeGetVpnConfig();

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
     * Connect button flow, Phase 3); only after the user accepts it
     * may the service be started.
     */
    public static Intent prepareIntent(Context context) {
        return VpnService.prepare(context);
    }

    @Override
    public void onCreate() {
        super.onCreate();
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
            teardownAsync();
            return START_NOT_STICKY;
        }
        if (ACTION_CONNECT.equals(action)) {
            if (connectThread != null && connectThread.isAlive()) {
                return START_NOT_STICKY;
            }
            // Foreground first: the system kills a service that does
            // not call startForeground promptly after start.
            startForegroundService(buildNotification("Skein VPN — Connecting", "Starting the mesh…"));
            connectThread = new Thread(this::connect, "skein-connect");
            connectThread.start();
            return START_NOT_STICKY;
        }
        return START_NOT_STICKY;
    }

    @Override
    public void onRevoke() {
        // The user revoked VPN permission in system settings (or
        // another VPN took over). Tear down; do not fight the system.
        Log.i(TAG, "VPN permission revoked, tearing down");
        teardownAsync();
        super.onRevoke();
    }

    @Override
    public void onDestroy() {
        closeTun();
        if (instance == this) instance = null;
        super.onDestroy();
    }

    private void connect() {
        boolean daemonStarted = false;
        try {
            if (nativeStartDaemon(getFilesDir().getAbsolutePath()) != 0) {
                Log.e(TAG, "nativeStartDaemon failed (see logcat: skein: android)");
                stopSelf();
                return;
            }
            daemonStarted = true;
            String configJson = nativeGetVpnConfig();
            if (configJson == null) {
                Log.e(TAG, "nativeGetVpnConfig failed (daemon not configured?)");
                teardownAsync();
                return;
            }
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
                Log.e(TAG, "establish() returned null (permission revoked or another VPN active?)");
                teardownAsync();
                return;
            }
            tunFd = pfd;
            int fd = pfd.detachFd();
            if (nativeSetTunFd(fd) != 0) {
                Log.e(TAG, "nativeSetTunFd failed");
                try {
                    ParcelFileDescriptor.adoptFd(fd).close();
                } catch (Exception ignored) {
                    // Best effort: the FD leaks only on this error path.
                }
                teardownAsync();
                return;
            }
            // Go now owns the FD; the daemon's TUN Close is what
            // eventually closes it.
            if (nativeConnectDaemon() != 0) {
                Log.e(TAG, "nativeConnectDaemon failed (see logcat: skein: android)");
                teardownAsync();
                return;
            }
            connected = true;
            String name = cfg.optString("name", "Skein");
            updateNotification("Skein VPN — Connected", "Connected to " + name);
        } catch (Exception e) {
            Log.e(TAG, "connect failed", e);
            if (daemonStarted) {
                teardownAsync();
            } else {
                stopSelf();
            }
        }
    }

    /** Disconnects on a worker thread: nativeDisconnectDaemon can block. */
    private void teardownAsync() {
        new Thread(() -> {
            connected = false;
            try {
                nativeDisconnectDaemon();
            } catch (Exception e) {
                Log.w(TAG, "nativeDisconnectDaemon failed", e);
            }
            closeTun();
            try {
                nativeStopDaemon();
            } catch (Exception e) {
                Log.w(TAG, "nativeStopDaemon failed", e);
            }
            stopForegroundCompat();
            stopSelf();
        }, "skein-disconnect").start();
    }

    /** Closes the (detached) ParcelFileDescriptor; the raw FD belongs to Go. */
    private void closeTun() {
        if (tunFd != null) {
            try {
                tunFd.close();
            } catch (Exception ignored) {
            }
            tunFd = null;
        }
    }

    private void createNotificationChannel() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) {
            return;
        }
        NotificationChannel channel = new NotificationChannel(
                CHANNEL_ID, "Skein VPN", NotificationManager.IMPORTANCE_LOW);
        channel.setDescription("Shows while the Skein mesh VPN is active");
        NotificationManager manager = getSystemService(NotificationManager.class);
        if (manager != null) {
            manager.createNotificationChannel(channel);
        }
    }

    private Notification buildNotification(String title, String text) {
        Intent activityIntent = new Intent(this, MainActivity.class);
        activityIntent.setFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP | Intent.FLAG_ACTIVITY_CLEAR_TOP);
        PendingIntent content = PendingIntent.getActivity(
                this, 0, activityIntent,
                PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);

        Intent disconnectIntent = new Intent(this, SkeinVpnService.class)
                .setAction(ACTION_DISCONNECT);
        PendingIntent disconnect = PendingIntent.getService(
                this, 0, disconnectIntent,
                PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);

        return new NotificationCompat.Builder(this, CHANNEL_ID)
                .setSmallIcon(R.mipmap.ic_launcher)
                .setContentTitle(title)
                .setContentText(text)
                .setContentIntent(content)
                .addAction(new NotificationCompat.Action.Builder(0, "Disconnect", disconnect).build())
                .setOngoing(true)
                .build();
    }

    private void startForegroundService(Notification notification) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            startForeground(NOTIFICATION_ID, notification,
                    ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE);
        } else {
            startForeground(NOTIFICATION_ID, notification);
        }
    }

    private void updateNotification(String title, String text) {
        NotificationManager manager = getSystemService(NotificationManager.class);
        if (manager != null) {
            manager.notify(NOTIFICATION_ID, buildNotification(title, text));
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
