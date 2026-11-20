package fi.sliqtly.warden;

import android.service.dreams.DreamService;
import android.webkit.WebView;

/**
 * The display as the tablet's screensaver: Android starts it while the
 * tablet charges (or is docked) and is idle, and a touch ends it.
 */
public class DisplayDream extends DreamService implements Display.Host {
    private WebView web;

    @Override
    public void onAttachedToWindow() {
        super.onAttachedToWindow();
        setInteractive(false);
        setFullscreen(true);
        setScreenBright(true);
        web = Display.make(this, this, false);
        setContentView(web);
    }

    @Override
    public void onDreamingStarted() {
        super.onDreamingStarted();
        Display.load(this, web);
    }

    @Override
    public void reload() {
        if (web != null) Display.load(this, web);
    }

    @Override
    public boolean located() {
        return checkSelfPermission(android.Manifest.permission.ACCESS_COARSE_LOCATION) == android.content.pm.PackageManager.PERMISSION_GRANTED;
    }

    @Override
    public void onDetachedFromWindow() {
        if (web != null) web.destroy();
        web = null;
        super.onDetachedFromWindow();
    }
}
