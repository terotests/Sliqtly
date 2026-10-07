// SPDX-License-Identifier: MIT
// After Carnivore (github.com/terotests/CarnivoreMusicPlayer, MIT), native/platform.h.
//
// What differs between desktops, for Sliqtly Editor.

#pragma once
#include <string>

// "mac" or "linux" (the app's shortcut names and its OAuth client).
const char* platformName();
// Where the settings are (created if missing): on macOS
// ~/Library/Application Support/Sliqtly Editor, elsewhere
// $XDG_CONFIG_HOME/sliqtly-editor (~/.config/sliqtly-editor).
// SLIQTLY_EDITOR_CONFIG overrides it (the checks use a folder of their own).
std::string platformConfigDir();
// Readable and writable by this user only (tokens are in it).
void platformPrivateFile(const std::string& path);
void platformPrivateDir(const std::string& path);
// The default browser.
void platformOpenUrl(const std::string& url);
// Write `text` to `path` through a temporary file, then make it private.
bool platformWritePrivate(const std::string& path, const std::string& text);
std::string platformReadFile(const std::string& path);
