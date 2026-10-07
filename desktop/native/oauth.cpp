// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Tero Tolonen (Carnivore; MIT licence text in desktop/LICENSE)
// See oauth.h. The listener is Carnivore's (github.com/terotests/CarnivoreMusicPlayer,
// MIT, native/spotify.cpp), on a port of the system's choosing.

#include "oauth.h"

#include <arpa/inet.h>
#include <netinet/in.h>
#include <poll.h>
#include <sys/socket.h>
#include <unistd.h>

#include <chrono>
#include <cstdio>
#include <cstdlib>
#include <random>

#include "platform.h"
#include "sha256.h"

namespace {

std::string randomString(size_t n) {
  static const char chars[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~";
  std::random_device rd;
  std::string out;
  for (size_t i = 0; i < n; i++) out += chars[rd() % (sizeof chars - 1)];
  return out;
}

// One query parameter out of "a=1&b=2", decoded.
std::string queryParam(const std::string& query, const std::string& key) {
  size_t pos = 0;
  while (pos <= query.size()) {
    size_t amp = query.find('&', pos);
    if (amp == std::string::npos) amp = query.size();
    std::string part = query.substr(pos, amp - pos);
    size_t eq = part.find('=');
    if (eq != std::string::npos && part.substr(0, eq) == key) {
      std::string v = part.substr(eq + 1), out;
      for (size_t i = 0; i < v.size(); i++) {
        if (v[i] == '%' && i + 2 < v.size()) {
          out += (char)std::strtol(v.substr(i + 1, 2).c_str(), nullptr, 16);
          i += 2;
        } else {
          out += v[i] == '+' ? ' ' : v[i];
        }
      }
      return out;
    }
    pos = amp + 1;
  }
  return "";
}

double now() { return std::chrono::duration<double>(std::chrono::steady_clock::now().time_since_epoch()).count(); }

std::string page(bool ok, const std::string& why) {
  std::string html = std::string("<!doctype html><meta charset=utf-8><title>Sliqtly Editor</title>"
                                 "<body style=\"font:16px system-ui;background:#121316;color:#e3dfd9;text-align:center;padding-top:22vh\">") +
                     (ok ? "<h1 style=\"color:#f3a066\">Signed in</h1><p>Sliqtly Editor is signed in. You can close this tab.</p>"
                         : "<h1>Not signed in</h1><p>" + why + "</p>") +
                     "</body>";
  return "HTTP/1.1 200 OK\r\nContent-Type: text/html; charset=utf-8\r\nContent-Length: " + std::to_string(html.size()) +
         "\r\nConnection: close\r\n\r\n" + html;
}

}  // namespace

OAuthLoopback::~OAuthLoopback() {
  stop_ = true;
  if (thread_.joinable()) thread_.join();
  if (fd_ >= 0) close(fd_);
}

bool OAuthLoopback::start(std::string& err) {
  if (running_) {
    err = "a sign-in is already waiting for the browser";
    return false;
  }
  if (thread_.joinable()) thread_.join();
  fd_ = socket(AF_INET, SOCK_STREAM, 0);
  sockaddr_in addr{};
  addr.sin_family = AF_INET;
  addr.sin_port = 0;
  addr.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
  socklen_t len = sizeof addr;
  if (fd_ < 0 || bind(fd_, (sockaddr*)&addr, sizeof addr) != 0 || listen(fd_, 4) != 0 ||
      getsockname(fd_, (sockaddr*)&addr, &len) != 0) {
    if (fd_ >= 0) close(fd_);
    fd_ = -1;
    err = "cannot listen on 127.0.0.1";
    return false;
  }
  redirect = "http://127.0.0.1:" + std::to_string(ntohs(addr.sin_port)) + "/callback";
  verifier = randomString(64);
  challenge = sq::base64url(sq::Sha256::digest(verifier));
  state = randomString(24);
  {
    std::lock_guard<std::mutex> g(lock_);
    done_ = false;
    result_ = Result();
  }
  stop_ = false;
  return true;
}

void OAuthLoopback::open(const std::string& url) {
  if (std::getenv("SLIQTLY_NO_BROWSER")) {
    std::printf("oauth: open %s\n", url.c_str());
    std::fflush(stdout);
  } else {
    platformOpenUrl(url);
  }
  running_ = true;
  thread_ = std::thread([this] { wait(); });
}

bool OAuthLoopback::take(Result& out) {
  std::lock_guard<std::mutex> g(lock_);
  if (!done_) return false;
  done_ = false;
  out = result_;
  return true;
}

void OAuthLoopback::wait() {
  Result r;
  r.error = "the browser did not come back in time";
  double until = now() + 300;
  while (now() < until && !stop_) {
    pollfd p{fd_, POLLIN, 0};
    if (::poll(&p, 1, 250) <= 0) continue;
    int c = accept(fd_, nullptr, nullptr);
    if (c < 0) continue;
    std::string req;
    char buf[2048];
    pollfd pc{c, POLLIN, 0};
    while (req.find("\r\n\r\n") == std::string::npos && req.size() < 16384 && ::poll(&pc, 1, 2000) > 0) {
      ssize_t n = recv(c, buf, sizeof buf, 0);
      if (n <= 0) break;
      req.append(buf, (size_t)n);
    }
    // "GET /callback?code=…&state=… HTTP/1.1"
    std::string target;
    size_t sp1 = req.find(' '), sp2 = sp1 == std::string::npos ? sp1 : req.find(' ', sp1 + 1);
    if (sp2 != std::string::npos) target = req.substr(sp1 + 1, sp2 - sp1 - 1);
    std::string path = target.substr(0, target.find('?'));
    std::string query = target.find('?') == std::string::npos ? "" : target.substr(target.find('?') + 1);
    if (path != "/callback") {
      std::string nf = "HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n";
      send(c, nf.data(), nf.size(), 0);
      close(c);
      continue;
    }
    std::string error = queryParam(query, "error");
    if (queryParam(query, "state") != state) {
      r.error = "the answer did not match (state)";
    } else if (!error.empty()) {
      std::string d = queryParam(query, "error_description");
      r.error = d.empty() ? error : error + ": " + d;
    } else {
      r.code = queryParam(query, "code");
      r.ok = !r.code.empty();
      if (!r.ok) r.error = "no code in the answer";
    }
    std::string answer = page(r.ok, r.error);
    send(c, answer.data(), answer.size(), 0);
    close(c);
    break;
  }
  close(fd_);
  fd_ = -1;
  {
    std::lock_guard<std::mutex> g(lock_);
    result_ = r;
    done_ = true;
  }
  running_ = false;
}
