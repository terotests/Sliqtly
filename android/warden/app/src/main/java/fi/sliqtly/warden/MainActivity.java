package fi.sliqtly.warden;

import android.app.Activity;
import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.content.pm.PackageManager;
import android.net.Uri;
import android.os.BatteryManager;
import android.os.Build;
import android.os.Bundle;
import android.view.View;
import android.view.WindowManager;
import android.webkit.WebView;

/**
 * The display, full screen. While the tablet charges the screen stays on
 * (no power: it may sleep as usual); with the app pinned (screen pinning)
 * the tablet is a wall display.
 */
public class MainActivity extends Activity implements Display.Host {
    private WebView web;
    private boolean charging;

    private final BroadcastReceiver power = new BroadcastReceiver() {
        @Override
        public void onReceive(Context c, Intent i) {
            int plugged = i.getIntExtra(BatteryManager.EXTRA_PLUGGED, 0);
            setCharging(plugged != 0);
        }
    };

    @Override
    protected void onCreate(Bundle b) {
        super.onCreate(b);
        if (Build.VERSION.SDK_INT >= 27) {
            setShowWhenLocked(true);
        } else {
            getWindow().addFlags(WindowManager.LayoutParams.FLAG_SHOW_WHEN_LOCKED);
        }
        web = Display.make(this, this, true);
        setContentView(web);
        take(getIntent());
        if (!located()) {
            requestPermissions(new String[]{android.Manifest.permission.ACCESS_COARSE_LOCATION, android.Manifest.permission.ACCESS_FINE_LOCATION}, 1);
        }
        Display.load(this, web);
    }

    @Override
    public boolean located() {
        return checkSelfPermission(android.Manifest.permission.ACCESS_COARSE_LOCATION) == PackageManager.PERMISSION_GRANTED;
    }

    @Override
    public void onRequestPermissionsResult(int code, String[] perms, int[] res) {
        super.onRequestPermissionsResult(code, perms, res);
        if (located()) Display.load(this, web);
    }

    @Override
    protected void onNewIntent(Intent i) {
        super.onNewIntent(i);
        if (take(i)) Display.load(this, web);
    }

    /** wardenhome://open?u=<the display's address>: kept, true when new */
    private boolean take(Intent i) {
        Uri d = i == null ? null : i.getData();
        if (d == null || !"wardenhome".equals(d.getScheme())) return false;
        return Display.save(this, d.getQueryParameter("u"));
    }

    @Override
    public void reload() {
        Display.load(this, web);
    }

    private void setCharging(boolean on) {
        charging = on;
        if (on) getWindow().addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
        else getWindow().clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
    }

    @Override
    protected void onResume() {
        super.onResume();
        Intent sticky = registerReceiver(power, new IntentFilter(Intent.ACTION_BATTERY_CHANGED));
        if (sticky != null) power.onReceive(this, sticky);
        immersive();
        web.onResume();
    }

    @Override
    protected void onPause() {
        unregisterReceiver(power);
        web.onPause();
        super.onPause();
    }

    @Override
    public void onWindowFocusChanged(boolean focus) {
        super.onWindowFocusChanged(focus);
        if (focus) immersive();
    }

    @SuppressWarnings("deprecation")
    private void immersive() {
        getWindow().getDecorView().setSystemUiVisibility(View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY
                | View.SYSTEM_UI_FLAG_FULLSCREEN | View.SYSTEM_UI_FLAG_HIDE_NAVIGATION
                | View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN | View.SYSTEM_UI_FLAG_LAYOUT_HIDE_NAVIGATION
                | View.SYSTEM_UI_FLAG_LAYOUT_STABLE);
    }

    @Override
    public void onBackPressed() {
        // the display has nowhere to go back to; the setup page goes back to it
        if (web.getUrl() != null && web.getUrl().startsWith(Display.SETUP) && Display.isDisplayUrl(Display.url(this))) {
            Display.load(this, web);
            return;
        }
        super.onBackPressed();
    }

    @Override
    protected void onDestroy() {
        web.destroy();
        super.onDestroy();
    }
}
