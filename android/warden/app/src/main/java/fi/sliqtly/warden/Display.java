package fi.sliqtly.warden;

import android.annotation.SuppressLint;
import android.content.Context;
import android.content.SharedPreferences;
import android.graphics.Color;
import android.net.Uri;
import android.os.Handler;
import android.os.Looper;
import android.webkit.JavascriptInterface;
import android.webkit.WebResourceError;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;

/**
 * The home display in a WebView: the address kept by the app (the display
 * page's, sliqtly.com/apps/warden/tv/&lt;token&gt;), or the setup page
 * (assets/setup.html) while there is none. Shared by the app's screen and
 * its screensaver.
 */
final class Display {
    static final String PREFS = "warden";
    static final String KEY_URL = "url";
    static final String SETUP = "file:///android_asset/setup.html";

    /** an address the app shows: Warden's display page only */
    static boolean isDisplayUrl(String u) {
        if (u == null) return false;
        Uri p = Uri.parse(u.trim());
        return "https".equals(p.getScheme()) && "sliqtly.com".equals(p.getHost())
                && p.getPath() != null && p.getPath().matches("/apps/warden/tv/[A-Za-z0-9_-]{16,64}/?");
    }

    static String url(Context c) {
        return c.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getString(KEY_URL, "");
    }

    static boolean save(Context c, String u) {
        if (!isDisplayUrl(u)) return false;
        c.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().putString(KEY_URL, u.trim()).apply();
        return true;
    }

    static void clear(Context c) {
        c.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().remove(KEY_URL).apply();
    }

    interface Host {
        /** the setup page asks for the display again (a new address saved) */
        void reload();
    }

    @SuppressLint({"SetJavaScriptEnabled", "AddJavascriptInterface"})
    static WebView make(final Context ctx, final Host host, boolean setupAllowed) {
        final WebView w = new WebView(ctx);
        w.setBackgroundColor(Color.parseColor("#0F1115"));
        WebSettings s = w.getSettings();
        s.setJavaScriptEnabled(true);
        s.setDomStorageEnabled(true);
        s.setCacheMode(WebSettings.LOAD_DEFAULT);
        s.setUserAgentString(s.getUserAgentString() + " WardenApp/1");
        s.setAllowFileAccess(false);
        final Handler h = new Handler(Looper.getMainLooper());
        w.setWebViewClient(new WebViewClient() {
            @Override
            public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest req) {
                // only the display and the setup page open here
                String u = req.getUrl().toString();
                return !(isDisplayUrl(u) || u.startsWith(SETUP));
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
        if (setupAllowed) {
            w.addJavascriptInterface(new Object() {
                @JavascriptInterface
                public boolean save(String u) {
                    boolean ok = Display.save(ctx, u);
                    if (ok) h.post(new Runnable() { public void run() { host.reload(); } });
                    return ok;
                }

                @JavascriptInterface
                public void setup() {
                    h.post(new Runnable() { public void run() { w.loadUrl(SETUP); } });
                }

                @JavascriptInterface
                public String current() {
                    return Display.url(ctx);
                }
            }, "WardenApp");
        }
        return w;
    }

    /** the display, or the setup page while there is no address */
    static void load(Context ctx, WebView w) {
        String u = url(ctx);
        w.loadUrl(isDisplayUrl(u) ? u : SETUP);
    }
}
