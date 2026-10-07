// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Tero Tolonen (Carnivore; MIT licence text in desktop/LICENSE)
// After Carnivore (github.com/terotests/CarnivoreMusicPlayer, MIT), native/platform.cpp.
// See platform.h.

#include "platform.h"

#include <sys/stat.h>

#include <cstdio>
#include <cstdlib>
#include <filesystem>
#include <fstream>
#include <sstream>

const char* platformName() {
#if defined(__APPLE__)
  return "mac";
#else
  return "linux";
#endif
}

std::string platformConfigDir() {
  std::string dir;
  const char* over = std::getenv("SLIQTLY_EDITOR_CONFIG");
  if (over && *over) {
    dir = over;
  } else {
    const char* home = std::getenv("HOME");
#if defined(__APPLE__)
    dir = std::string(home ? home : ".") + "/Library/Application Support/Sliqtly Editor";
#else
    const char* xdg = std::getenv("XDG_CONFIG_HOME");
    dir = (xdg && *xdg ? std::string(xdg) : std::string(home ? home : ".") + "/.config") + "/sliqtly-editor";
#endif
  }
  std::error_code ec;
  std::filesystem::create_directories(dir, ec);
  platformPrivateDir(dir);
  return dir;
}

void platformPrivateFile(const std::string& path) { chmod(path.c_str(), 0600); }
void platformPrivateDir(const std::string& path) { chmod(path.c_str(), 0700); }

namespace {
// Single quotes for sh: ' becomes '\''.
std::string shellQuote(const std::string& s) {
  std::string out = "'";
  for (char c : s) {
    if (c == '\'') out += "'\\''";
    else out += c;
  }
  return out + "'";
}
}  // namespace

void platformOpenUrl(const std::string& url) {
#if defined(__APPLE__)
  std::system(("open " + shellQuote(url) + " >/dev/null 2>&1").c_str());
#else
  std::system(("xdg-open " + shellQuote(url) + " >/dev/null 2>&1 &").c_str());
#endif
}

bool platformWritePrivate(const std::string& path, const std::string& text) {
  std::string tmp = path + ".tmp";
  {
    std::ofstream f(tmp, std::ios::trunc | std::ios::binary);
    if (!f) return false;
    platformPrivateFile(tmp);
    f << text;
    if (!f) return false;
  }
  platformPrivateFile(tmp);
  return std::rename(tmp.c_str(), path.c_str()) == 0;
}

std::string platformReadFile(const std::string& path) {
  std::ifstream f(path, std::ios::binary);
  std::stringstream ss;
  ss << f.rdbuf();
  return ss.str();
}
