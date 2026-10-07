// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Tero Tolonen (Carnivore; MIT licence text in desktop/LICENSE)
// From Carnivore (github.com/terotests/CarnivoreMusicPlayer, MIT), native/http.h,
// with what a Sliqtly server needs on top: a timeout in milliseconds, a pinned
// CA (libcurl's CAINFO) and, for reading one server's CA fingerprint only,
// a request that does not verify the certificate.
//
// HTTP(S) through libcurl (part of macOS; libcurl4-openssl-dev on
// Debian/Ubuntu). Blocking: call it from a worker thread, never from the
// frame loop.

#pragma once
#include <string>
#include <vector>

struct HttpResponse {
  long status = 0;        // 0 when no answer came (see `error`)
  std::string body;
  std::string error;      // the transport's reason when status is 0
};

struct HttpOptions {
  long timeoutMs = 20000;
  std::string caFile;     // a PEM file of the CA to trust (and only it), or ""
  bool insecure = false;  // do not verify the peer (the CA fingerprint read only)
};

// `headers` are whole lines ("Authorization: Bearer …"). A POST or PUT with
// an empty body still sends Content-Length: 0.
HttpResponse httpRequest(const std::string& method, const std::string& url, const std::vector<std::string>& headers,
                         const std::string& body, const HttpOptions& opts = HttpOptions());
