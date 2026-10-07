// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Tero Tolonen (Carnivore; MIT licence text in desktop/LICENSE)
// SHA-256, base64url and base64 decoding: for PKCE (oauth.cpp) and for
// checking a server's CA certificate against its fingerprint (host.cpp).
// SHA-256 and base64url from Carnivore (github.com/terotests/CarnivoreMusicPlayer,
// MIT), native/spotify.cpp.

#pragma once
#include <cstdint>
#include <string>

namespace sq {

struct Sha256 {
  uint32_t h[8] = {0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19};
  static uint32_t rotr(uint32_t x, int n) { return (x >> n) | (x << (32 - n)); }
  void block(const unsigned char* p) {
    static const uint32_t k[64] = {
        0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01,
        0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc,
        0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da, 0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147,
        0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
        0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070, 0x19a4c116, 0x1e376c08,
        0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
        0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2};
    uint32_t w[64];
    for (int i = 0; i < 16; i++) w[i] = (uint32_t)p[i * 4] << 24 | (uint32_t)p[i * 4 + 1] << 16 | (uint32_t)p[i * 4 + 2] << 8 | p[i * 4 + 3];
    for (int i = 16; i < 64; i++) {
      uint32_t s0 = rotr(w[i - 15], 7) ^ rotr(w[i - 15], 18) ^ (w[i - 15] >> 3);
      uint32_t s1 = rotr(w[i - 2], 17) ^ rotr(w[i - 2], 19) ^ (w[i - 2] >> 10);
      w[i] = w[i - 16] + s0 + w[i - 7] + s1;
    }
    uint32_t a = h[0], b = h[1], c = h[2], d = h[3], e = h[4], f = h[5], g = h[6], hh = h[7];
    for (int i = 0; i < 64; i++) {
      uint32_t t1 = hh + (rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25)) + ((e & f) ^ (~e & g)) + k[i] + w[i];
      uint32_t t2 = (rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22)) + ((a & b) ^ (a & c) ^ (b & c));
      hh = g;
      g = f;
      f = e;
      e = d + t1;
      d = c;
      c = b;
      b = a;
      a = t1 + t2;
    }
    h[0] += a, h[1] += b, h[2] += c, h[3] += d, h[4] += e, h[5] += f, h[6] += g, h[7] += hh;
  }
  static std::string digest(const std::string& msg) {
    Sha256 s;
    std::string m = msg;
    uint64_t bits = (uint64_t)msg.size() * 8;
    m += (char)0x80;
    while (m.size() % 64 != 56) m += (char)0;
    for (int i = 7; i >= 0; i--) m += (char)((bits >> (i * 8)) & 0xff);
    for (size_t i = 0; i < m.size(); i += 64) s.block((const unsigned char*)m.data() + i);
    std::string out;
    for (uint32_t v : s.h)
      for (int i = 3; i >= 0; i--) out += (char)((v >> (i * 8)) & 0xff);
    return out;
  }
};

inline std::string hex(const std::string& bytes) {
  static const char* h = "0123456789abcdef";
  std::string out;
  for (unsigned char c : bytes) {
    out += h[c >> 4];
    out += h[c & 15];
  }
  return out;
}

inline std::string base64url(const std::string& in) {
  static const char tab[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
  std::string out;
  size_t i = 0;
  for (; i + 2 < in.size(); i += 3) {
    uint32_t v = (unsigned char)in[i] << 16 | (unsigned char)in[i + 1] << 8 | (unsigned char)in[i + 2];
    out += tab[v >> 18], out += tab[(v >> 12) & 63], out += tab[(v >> 6) & 63], out += tab[v & 63];
  }
  if (i + 1 == in.size()) {
    uint32_t v = (unsigned char)in[i] << 16;
    out += tab[v >> 18], out += tab[(v >> 12) & 63];
  } else if (i + 2 == in.size()) {
    uint32_t v = (unsigned char)in[i] << 16 | (unsigned char)in[i + 1] << 8;
    out += tab[v >> 18], out += tab[(v >> 12) & 63], out += tab[(v >> 6) & 63];
  }
  return out;
}

// Standard base64 (whitespace skipped), as a PEM body is written.
inline std::string base64Decode(const std::string& in) {
  std::string out;
  uint32_t acc = 0;
  int bits = 0;
  for (unsigned char c : in) {
    int v;
    if (c >= 'A' && c <= 'Z') v = c - 'A';
    else if (c >= 'a' && c <= 'z') v = c - 'a' + 26;
    else if (c >= '0' && c <= '9') v = c - '0' + 52;
    else if (c == '+' || c == '-') v = 62;
    else if (c == '/' || c == '_') v = 63;
    else continue;
    acc = (acc << 6) | (uint32_t)v;
    bits += 6;
    if (bits >= 8) {
      bits -= 8;
      out += (char)((acc >> bits) & 0xff);
    }
  }
  return out;
}

// The DER bytes of the first certificate in a PEM text ("" when there is none).
inline std::string pemFirstCertificateDer(const std::string& pem) {
  const std::string begin = "-----BEGIN CERTIFICATE-----", end = "-----END CERTIFICATE-----";
  size_t a = pem.find(begin);
  if (a == std::string::npos) return "";
  a += begin.size();
  size_t b = pem.find(end, a);
  if (b == std::string::npos) return "";
  return base64Decode(pem.substr(a, b - a));
}

}  // namespace sq
