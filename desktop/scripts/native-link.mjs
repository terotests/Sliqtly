// SPDX-License-Identifier: MIT
//
// What both native builds share (scripts/build-editor-native.mjs, the editor;
// scripts/build-native.mjs, the earlier EditorApp): finding the C++ compiler
// and SDL2, the macOS flags, and the .app bundle.
//
// SDL2_FRAMEWORK=<path>/SDL2.framework (the official one, from the SDL2 .dmg)
// is linked and copied into the bundle, so the app needs no Homebrew; with
// UNIVERSAL=1 the binary is for both Apple silicon and Intel. Without it
// pkg-config's SDL2 is linked, which is fine on the machine that built it.

import fs from "node:fs";
import path from "node:path";
import { execFileSync, execSync, spawnSync } from "node:child_process";

export const MAC = process.platform === "darwin";
// macOS: the oldest system the app opens on. Without it the binary takes the
// build machine's version (a Tahoe runner made an app nothing older opens).
export const MAC_MIN = "11.0";

const die = (msg) => { console.error(msg); process.exit(1); };
export const has = (cmd) => spawnSync("sh", ["-c", `command -v ${cmd}`]).status === 0;

export function cxx() {
  const c = process.env.CXX || (MAC ? ["clang++", "g++"] : ["g++", "clang++"]).find(has);
  if (!c) die("no C++ compiler (clang++ / g++) found");
  return c;
}

/** { flags, framework }: how to compile and link SDL2 here. */
export function sdl2() {
  const framework = MAC ? (process.env.SDL2_FRAMEWORK || "") : "";
  if (framework && !fs.existsSync(path.join(framework, "Headers", "SDL.h"))) die(`SDL2_FRAMEWORK is not an SDL2.framework: ${framework}`);
  let flags = "";
  if (framework) {
    flags = `-I${JSON.stringify(path.join(framework, "Headers"))} -F${JSON.stringify(path.dirname(framework))} -framework SDL2 -rpath @executable_path/../Frameworks`;
  }
  if (!flags) { try { flags = execSync("pkg-config --cflags --libs sdl2", { encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] }).trim(); } catch {} }
  if (!flags) { try { flags = execSync("sdl2-config --cflags --libs", { encoding: "utf8" }).trim(); } catch {} }
  if (!flags) die(MAC ? "SDL2 not found: brew install sdl2" : "SDL2 not found: sudo apt-get install libsdl2-dev");
  return { flags, framework };
}

export const glFlags = () => (MAC ? "-framework OpenGL -framework Cocoa" : "-lGL");

/** The macOS target flags, before the sources. */
export function macFlags(framework) {
  if (!MAC) return "";
  return `-mmacosx-version-min=${MAC_MIN}${framework && process.env.UNIVERSAL === "1" ? " -arch arm64 -arch x86_64" : ""} `;
}

/**
 * <buildDir>/<name>.app around `bin`, run as Contents/MacOS/<exe>.
 * `resources` are folders copied into Contents/Resources under their own
 * names (SDL_GetBasePath() is Contents/Resources/ inside a bundle). The icon
 * is made from native/icon/icon-1024.png with sips / iconutil, or the
 * committed AppIcon.icns. Signed ad hoc: Apple silicon runs nothing unsigned.
 */
export function macApp({ nativeDir, buildDir, name, bundleId, version, bin, exe, resources = [], framework = "" }) {
  const bundle = path.join(buildDir, `${name}.app`);
  const app = path.join(bundle, "Contents");
  fs.rmSync(bundle, { recursive: true, force: true });
  fs.mkdirSync(path.join(app, "MacOS"), { recursive: true });
  fs.mkdirSync(path.join(app, "Resources"), { recursive: true });
  fs.copyFileSync(bin, path.join(app, "MacOS", exe));
  fs.chmodSync(path.join(app, "MacOS", exe), 0o755);
  for (const dir of resources) fs.cpSync(dir, path.join(app, "Resources", path.basename(dir)), { recursive: true });
  const iconPng = path.join(nativeDir, "icon", "icon-1024.png");
  const iconset = path.join(buildDir, "AppIcon.iconset");
  let icon = false;
  if (fs.existsSync(iconPng) && has("sips") && has("iconutil")) {
    fs.rmSync(iconset, { recursive: true, force: true });
    fs.mkdirSync(iconset);
    for (const s of [16, 32, 128, 256, 512]) {
      for (const [mul, suffix] of [[1, ""], [2, "@2x"]]) {
        const out = path.join(iconset, `icon_${s}x${s}${suffix}.png`);
        execFileSync("sips", ["-z", String(s * mul), String(s * mul), iconPng, "--out", out], { stdio: "ignore" });
      }
    }
    execFileSync("iconutil", ["-c", "icns", iconset, "-o", path.join(app, "Resources", "AppIcon.icns")]);
    icon = true;
  } else if (fs.existsSync(path.join(nativeDir, "icon", "AppIcon.icns"))) {
    fs.copyFileSync(path.join(nativeDir, "icon", "AppIcon.icns"), path.join(app, "Resources", "AppIcon.icns"));
    icon = true;
  }
  fs.writeFileSync(path.join(app, "Info.plist"), `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleName</key><string>${name}</string>
  <key>CFBundleDisplayName</key><string>${name}</string>
  <key>CFBundleIdentifier</key><string>${bundleId}</string>
  <key>CFBundleExecutable</key><string>${exe}</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>${version}</string>
  <key>CFBundleVersion</key><string>${version}</string>
  <key>LSMinimumSystemVersion</key><string>${MAC_MIN}</string>
  <key>LSApplicationCategoryType</key><string>public.app-category.productivity</string>
  <key>NSHighResolutionCapable</key><true/>${icon ? "\n  <key>CFBundleIconFile</key><string>AppIcon</string>" : ""}
</dict></plist>
`);
  if (framework) {
    // ditto keeps the framework's symlinks and its signature
    fs.mkdirSync(path.join(app, "Frameworks"), { recursive: true });
    execFileSync("ditto", [framework, path.join(app, "Frameworks", "SDL2.framework")]);
  }
  if (has("codesign")) execFileSync("codesign", ["--force", "--deep", "--sign", "-", bundle], { stdio: "ignore" });
  return { bundle, icon };
}
