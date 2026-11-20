package fi.sliqtly.warden;

import android.annotation.SuppressLint;
import android.content.Context;
import android.content.SharedPreferences;
import android.graphics.Color;
import android.net.Uri;
import android.os.Handler;
import android.os.Looper;
import android.util.Base64;
import android.webkit.GeolocationPermissions;
import android.webkit.JavascriptInterface;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceError;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;

import java.security.SecureRandom;

/**
 * The home display (sliqtly.com/apps/warden/tv/) in a WebView. The page
 * shows a space only to a paired device: this app's secret, made on its
 * first start and kept in its private storage, goes with every call the
 * page makes (Authorization: Bearer, over HTTPS). Until a member pairs it,
 * the page shows a code to enter in Warden. Shared by the app's screen and
 * its screensaver.
 */
final class Display {
    static final String URL = "https://sliqtly.com/apps/warden/tv/";
    private static final String PREFS = "warden";
    private static final String KEY = "device_key";

    /** a page the app shows: Warden's display only */
    static boolean isDisplayUrl(String u) {
        if (u == null) return false;
        Uri p = Uri.parse(u);
        return "https".equals(p.getScheme()) && "sliqtly.com".equals(p.getHost())
                && p.getPath() != null && p.getPath().startsWith("/apps/warden/tv");
    }

    /** this device's secret, made once */
    static synchronized String deviceKey(Context c) {
        SharedPreferences sp = c.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
        String k = sp.getString(KEY, "");
        if (k.length() < 32) {
            byte[] b = new byte[32];
            new SecureRandom().nextBytes(b);
            k = Base64.encodeToString(b, Base64.URL_SAFE | Base64.NO_PADDING | Base64.NO_WRAP);
            sp.edit().putString(KEY, k).apply();
        }
        return k;
    }

    interface Host {
        /** the display again (after no network) */
        void reload();

        /** whether the app may tell the page where the tablet is */
        boolean located();
    }

    @SuppressLint({"SetJavaScriptEnabled", "AddJavascriptInterface"})
    static WebView make(final Context ctx, final Host host) {
        final WebView w = new WebView(ctx);
        w.setBackgroundColor(Color.parseColor("#0F1115"));
        WebSettings s = w.getSettings();
        s.setJavaScriptEnabled(true);
        s.setDomStorageEnabled(true);
        s.setCacheMode(WebSettings.LOAD_DEFAULT);
        s.setUserAgentString(s.getUserAgentString() + " WardenApp/2");
        s.setAllowFileAccess(false);
        s.setGeolocationEnabled(true);
        final Handler h = new Handler(Looper.getMainLooper());
        // the display's page may know the tablet's place (the nearest stops)
        // when the app has the location permission
        w.setWebChromeClient(new WebChromeClient() {
            @Override
            public void onGeolocationPermissionsShowPrompt(String origin, GeolocationPermissions.Callback cb) {
                Uri o = Uri.parse(origin);
                cb.invoke(origin, "https".equals(o.getScheme()) && "sliqtly.com".equals(o.getHost()) && host.located(), false);
            }
        });
        w.setWebViewClient(new WebViewClient() {
            @Override
            public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest req) {
                // only the display opens here
                return !isDisplayUrl(req.getUrl().toString());
            }

            @Override
            public void onReceivedError(WebView view, WebResourceRequest req, WebResourceError err) {
                // no network (the router restarting): the display again in half a minute
                if (req.isForMainFrame()) {
                    view.loadData("<body style='background:#0F1115;color:#9aa1ad;font:20px sans-serif;display:flex;align-items:center;justify-content:center;height:90vh'>Ei verkkoyhteyttä. Yritetään uudelleen…</body>", "text/html; charset=utf-8", "utf-8");
                    h.postDelayed(new Runnable() { public void run() { host.reload(); } }, 30000);
                }
            }
        });
        // the secret, for the display's own page only
        w.addJavascriptInterface(new Object() {
            @JavascriptInterface
            public String deviceKey() {
                return isDisplayUrl(currentUrl[0]) ? Display.deviceKey(ctx) : "";
            }
        }, "WardenApp");
        return w;
    }

    // the page shown, as the JavaScript bridge (on another thread) may read it
    private static final String[] currentUrl = {URL};

    static void load(WebView w) {
        currentUrl[0] = URL;
        w.loadUrl(URL);
    }
}
