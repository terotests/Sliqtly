// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Tero Tolonen (Carnivore; MIT licence text in desktop/LICENSE)
// From Carnivore (github.com/terotests/CarnivoreMusicPlayer, MIT), native/http_curl.cpp.
// See http.h.

#include "http.h"

#include <curl/curl.h>

#include <mutex>

namespace {

std::once_flag curlInit;

size_t onBody(char* data, size_t size, size_t n, void* user) {
  static_cast<std::string*>(user)->append(data, size * n);
  return size * n;
}

}  // namespace

HttpResponse httpRequest(const std::string& method, const std::string& url, const std::vector<std::string>& headers,
                         const std::string& body, const HttpOptions& opts) {
  std::call_once(curlInit, [] { curl_global_init(CURL_GLOBAL_DEFAULT); });
  HttpResponse res;
  CURL* c = curl_easy_init();
  if (!c) {
    res.error = "curl_easy_init failed";
    return res;
  }
  struct curl_slist* list = nullptr;
  for (auto& h : headers) list = curl_slist_append(list, h.c_str());
  // libcurl adds "Expect: 100-continue" to larger bodies; a plain server
  // answers faster without the round trip.
  list = curl_slist_append(list, "Expect:");
  curl_easy_setopt(c, CURLOPT_URL, url.c_str());
  curl_easy_setopt(c, CURLOPT_HTTPHEADER, list);
  curl_easy_setopt(c, CURLOPT_TIMEOUT_MS, opts.timeoutMs);
  curl_easy_setopt(c, CURLOPT_CONNECTTIMEOUT_MS, opts.timeoutMs < 10000 ? opts.timeoutMs : 10000L);
  curl_easy_setopt(c, CURLOPT_NOSIGNAL, 1L);
  curl_easy_setopt(c, CURLOPT_WRITEFUNCTION, onBody);
  curl_easy_setopt(c, CURLOPT_WRITEDATA, &res.body);
  curl_easy_setopt(c, CURLOPT_USERAGENT, "SliqtlyEditor/0.1");
  curl_easy_setopt(c, CURLOPT_ACCEPT_ENCODING, "");
  if (opts.insecure) {
    curl_easy_setopt(c, CURLOPT_SSL_VERIFYPEER, 0L);
    curl_easy_setopt(c, CURLOPT_SSL_VERIFYHOST, 0L);
  } else if (!opts.caFile.empty()) {
    curl_easy_setopt(c, CURLOPT_CAINFO, opts.caFile.c_str());
  }
  if (method == "GET") {
    curl_easy_setopt(c, CURLOPT_HTTPGET, 1L);
  } else {
    curl_easy_setopt(c, CURLOPT_CUSTOMREQUEST, method.c_str());
    curl_easy_setopt(c, CURLOPT_POSTFIELDS, body.c_str());
    curl_easy_setopt(c, CURLOPT_POSTFIELDSIZE, (long)body.size());
  }
  CURLcode rc = curl_easy_perform(c);
  if (rc == CURLE_OK) {
    curl_easy_getinfo(c, CURLINFO_RESPONSE_CODE, &res.status);
  } else {
    res.error = curl_easy_strerror(rc);
  }
  curl_slist_free_all(list);
  curl_easy_cleanup(c);
  return res;
}
