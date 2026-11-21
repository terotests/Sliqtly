# SPDX-License-Identifier: AGPL-3.0-or-later
# Draws assets/icon.png (1024x1024): the Studio's app icon. A planet with a
# ring and an orbiting ship over a dark rounded square, in the Studio's colours.
# Run: python3 assets/make_icon.py (needs Pillow).
import math, os
from PIL import Image, ImageDraw, ImageFilter
S = 1024
k = 4  # supersampling
W = S * k
img = Image.new("RGBA", (W, W), (0, 0, 0, 0))
d = ImageDraw.Draw(img)
m = 90 * k
d.rounded_rectangle([m, m, W - m, W - m], radius=190 * k, fill=(18, 20, 30, 255))
# stars
import random
random.seed(7)
for _ in range(60):
    x, y = random.randint(m + 60 * k, W - m - 60 * k), random.randint(m + 60 * k, W - m - 60 * k)
    r = random.choice([3, 4, 6]) * k // 2
    d.ellipse([x - r, y - r, x + r, y + r], fill=(206, 214, 234, random.randint(90, 220)))
cx, cy, R = W // 2, W // 2 + 20 * k, 250 * k
# ring behind
ring = Image.new("RGBA", (W, W), (0, 0, 0, 0))
rd = ImageDraw.Draw(ring)
rd.ellipse([cx - 430 * k, cy - 120 * k, cx + 430 * k, cy + 120 * k], outline=(224, 175, 104, 255), width=34 * k)
ring = ring.rotate(-18, center=(cx, cy), resample=Image.BICUBIC)
back = ring.copy()
bd = ImageDraw.Draw(back)
# keep only the upper half of the ring behind the planet
mask = Image.new("L", (W, W), 0)
md = ImageDraw.Draw(mask)
md.polygon([(0, 0), (W, 0), (W, cy - 40 * k + int(math.tan(math.radians(18)) * W / 2)), (0, cy - 40 * k - int(math.tan(math.radians(18)) * W / 2))], fill=255)
img.alpha_composite(Image.composite(back, Image.new("RGBA", (W, W)), mask))
# planet with light from the upper left
planet = Image.new("RGBA", (W, W), (0, 0, 0, 0))
pd = ImageDraw.Draw(planet)
steps = 60
for i in range(steps):
    t = i / steps
    rr = int(R * (1 - t * 0.55))
    ox, oy = int(-R * 0.35 * t), int(-R * 0.35 * t)
    c = (int(61 + (122 - 61) * t), int(89 + (162 - 89) * t), int(161 + (247 - 161) * t), 255)
    pd.ellipse([cx + ox - rr, cy + oy - rr, cx + ox + rr, cy + oy + rr], fill=c)
pm = Image.new("L", (W, W), 0)
ImageDraw.Draw(pm).ellipse([cx - R, cy - R, cx + R, cy + R], fill=255)
img.alpha_composite(Image.composite(planet, Image.new("RGBA", (W, W)), pm))
# ring in front (lower half)
inv = Image.eval(mask, lambda v: 255 - v)
img.alpha_composite(Image.composite(ring, Image.new("RGBA", (W, W)), inv))
# a small ship with a green trail
sx, sy = cx + 300 * k, cy - 300 * k
d = ImageDraw.Draw(img)
d.line([(sx - 240 * k, sy + 60 * k), (sx - 20 * k, sy + 5 * k)], fill=(158, 206, 106, 200), width=10 * k)
d.polygon([(sx + 50 * k, sy - 10 * k), (sx - 30 * k, sy - 40 * k), (sx - 10 * k, sy + 30 * k)], fill=(235, 240, 250, 255))
img = img.resize((S, S), Image.LANCZOS)
out = os.path.join(os.path.dirname(os.path.abspath(__file__)), "icon.png")
img.save(out)
print(out)
