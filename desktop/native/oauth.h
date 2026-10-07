// SPDX-License-Identifier: MIT
//
// Signing in to a Sliqtly server from the desktop: OAuth 2.1's authorization
// code flow with PKCE (S256), the client `sliqtly-desktop`, and a redirect to
// http://127.0.0.1:<a free port>/callback that this file answers. After
// Carnivore's Spotify sign-in (github.com/terotests/CarnivoreMusicPlayer, MIT,
// native/spotify.cpp), whose loopback listener and PKCE this reuses.
//
// The app (Session.rgr) builds the authorize URL and exchanges the code; this
// file makes the verifier, the challenge and the state, opens the browser
// and waits on its own thread for the browser to come back.
//
//   OAuthLoopback sign;
//   if (sign.start()) { url = app->oauthAuthorizeUrl(sign.challenge, sign.state, sign.redirect); sign.open(url); }
//   … every frame: if (sign.take(result)) app->oauthCode(result.code, sign.verifier, sign.redirect)

#pragma once
#include <atomic>
#include <mutex>
#include <string>
#include <thread>

class OAuthLoopback {
 public:
  struct Result {
    bool ok = false;
    std::string code;
    std::string error;
  };

  ~OAuthLoopback();
  // Listen on 127.0.0.1 (a port the system picks); false if it cannot.
  bool start(std::string& err);
  // Open the browser at `url` (SLIQTLY_NO_BROWSER: print it instead, for the
  // checks) and wait for /callback, five minutes at most.
  void open(const std::string& url);
  // The browser came back (or gave up): once.
  bool take(Result& out);
  bool active() const { return running_.load(); }

  std::string verifier, challenge, state, redirect;

 private:
  void wait();
  int fd_ = -1;
  std::thread thread_;
  std::atomic<bool> running_{false}, stop_{false};
  std::mutex lock_;
  bool done_ = false;
  Result result_;
};
