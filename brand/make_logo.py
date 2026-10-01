import math

# The wordmark: the letters of the trace's silhouette layer (#081430), as traced.
LETTERS = (
    "M363.64 60.89c-4.5 2.98-6.64 7-6.64 12.48c0 4.32.44 5.3 3.88 8.75c3.61 3.6 4.29 3.88 9.62 3.88s6.01-.28 9.62-3.88c3.56-3.57 3.88-4.33 3.88-9.37c0-4.04-.55-6.18-2.09-8.14c-4.42-5.61-12.82-7.32-18.27-3.72Z"
    "m-39.72 3.83c-.67 1.76-.93 17.32-.73 44.25c.34 46.18.28 45.73 7.15 51.75c3.64 3.2 11.78 6.28 16.59 6.28c4.64 0 5.5-1.36 5.9-9.31c.37-7.38.36-7.44-3.23-11.02l-3.6-3.6v-38.6c0-24.7-.37-39.3-1.04-40.54c-.91-1.7-2.16-1.93-10.52-1.93c-9.41 0-9.5.02-10.52 2.72Z"
    "M519.2 63.2c-.9.9-1.2 11.99-1.2 43.75c0 40.47.1 42.76 2 46.8c3.68 7.82 10.05 11.76 20.84 12.89c3.7.39 6.63.18 8.03-.57c2.05-1.1 2.19-1.79 1.92-9.88l-.29-8.69l-3.29-.63c-1.81-.35-4.17-1.43-5.25-2.41c-1.88-1.7-1.96-3.31-1.96-40.91c0-29.12-.31-39.46-1.2-40.35c-.75-.75-4.44-1.2-9.8-1.2s-9.05.45-9.8 1.2Z"
    "M268.25 67.48C252.05 72.04 243 82.69 243 97.17c0 17.29 10.01 25.73 36.7 30.94c11.82 2.31 16.07 6.58 12.81 12.87c-2.4 4.65-7.24 6.32-17 5.88c-8.12-.37-12.79-2.04-19.14-6.86c-1.45-1.1-3.43-2-4.39-2s-4.35 3.17-7.51 7.04l-5.76 7.04l2.75 2.88c3.4 3.55 12.64 8.46 20.29 10.79c7 2.13 22.49 2 30.75-.25c12.46-3.4 21.32-11.69 24.25-22.69c2.18-8.19 1.07-15.84-3.25-22.31c-4.63-6.94-11.96-10.64-27.75-14c-12.97-2.76-18.22-5.24-19.18-9.07C263.76 86.22 284.71 81.92 297.9 91c1.6 1.1 3.74 2 4.75 2c1.65 0 11.29-10.33 12.21-13.07c1.04-3.14-9.84-9.79-20.32-12.43c-7.6-1.91-19.54-1.92-26.29-.02Z"
    "m206.5 5.57c-1.45.87-1.75 2.52-1.75 9.5V91h-3.31c-5.23 0-7.09 1.94-6.33 6.59c.48 2.91 1.78 4.96 5.13 8.02l4.48 4.11l-.02 18.88c-.02 18.69 0 18.93 2.76 24.39c4.71 9.32 14.17 14.01 28.23 14.01c8.87 0 10.12-1.47 9.81-11.5l-.25-8l-6.44-.61c-5.32-.51-6.83-1.08-8.75-3.31c-2.2-2.56-2.31-3.43-2.31-18.14V110h5.07c8.16 0 8.93-.82 8.93-9.5s-.77-9.5-8.93-9.5H496V83.5c0-10.29-1.24-11.5-11.75-11.5c-4.26 0-8.54.47-9.5 1.05Z"
    "M547.67 92.67c-.37.36-.67 1.75-.67 3.07c0 3.56 13.67 57.71 15.42 61.11c2.02 3.9 6.83 7.83 10.62 8.66c8.89 1.95.61 11.49-9.97 11.49c-2.93 0-4.28.52-5.03 1.93c-1.26 2.35-1.38 14.04-.17 15.91c.88 1.37 13.18 1.81 18.64.68c7.3-1.52 16.2-9.1 20.45-17.45c1.99-3.9 5.13-14.4 12.04-40.32c1.65-6.19 4.13-15.3 5.5-20.25c4.58-16.5 5.83-22.21 5.19-23.86c-.52-1.36-2.23-1.64-9.91-1.64c-5.1 0-9.81.35-10.47.77c-1.52.99-2.93 4.65-4.81 12.48c-.83 3.44-2.4 9.4-3.5 13.25s-2.93 10.89-4.06 15.64s-2.66 9.46-3.39 10.46c-1.6 2.18-1.09 3.77-9-27.94c-3-12.01-5.98-22.47-6.62-23.25c-.86-1.03-3.68-1.41-10.39-1.41c-5.06 0-9.51.3-9.87.67Z"
    "M359.65 94.83c-1.47 1.62-1.65 5.56-1.65 35.34c0 39.35-.75 36.83 10.89 36.83c12.13 0 11.11 3.43 11.11-37.43c0-31.32-.18-35.43-1.58-35.96c-.87-.34-5.08-.61-9.35-.61c-6.25 0-8.08.36-9.42 1.83Z"
)
# the i's dot is the first sub-path
DOT, REST = LETTERS.split("Z", 1)
DOT += "Z"
REST = "M323.92 64.72" + REST[len("m-39.72 3.83"):]
# the q, drawn in place of the traced Q: a bowl of the letters' stroke and a
# stem whose foot turns out to the right the way the y's tail turns left
Q_BOWL = (
    "M423 90a37.5 38.5 0 1 0 0.001 0Z"   # outside
    "M423 110.5a17 18 0 1 1 -0.001 0Z"   # the counter
)
Q_STEM = (
    "M440 93.5Q440 90.5 443 90.5L458 90.5Q461 90.5 461 93.5L461 165"
    "C461 173.5 465 178 472.5 178L476 178Q479 178 479 181L479 193Q479 196 476 196L463 196"
    "C448.5 196 440 187 440 172Z"
)


def rounded(points, r):
    """A closed polygon with every corner rounded (quadratic, radius r)."""
    n = len(points)
    out = []
    for i in range(n):
        p0, p1, p2 = points[i - 1], points[i], points[(i + 1) % n]
        def toward(a, b, d):
            dx, dy = b[0] - a[0], b[1] - a[1]
            L = math.hypot(dx, dy)
            k = min(d, L / 2) / L
            return (a[0] + dx * k, a[1] + dy * k)
        a = toward(p1, p0, r)
        b = toward(p1, p2, r)
        out.append(("L" if out else "M") + f"{a[0]:.2f} {a[1]:.2f}")
        out.append(f"Q{p1[0]:.2f} {p1[1]:.2f} {b[0]:.2f} {b[1]:.2f}")
    return "".join(out) + "Z"


# the card: tall on the left, its top and bottom closing in toward the right
def card(left, scale, cy=112.5):
    w, hl, hr = 139 * scale, 177 * scale, 120 * scale
    pts = [(left, cy - hl / 2), (left + w, cy - hr / 2), (left + w, cy + hr / 2), (left, cy + hl / 2)]
    return rounded(pts, 24 * scale)


ICON = f"""
  <path d="{card(21, 0.42)}" fill="url(#g-ghost1)"/>
  <path d="{card(49, 0.73)}" fill="url(#g-ghost2)"/>
  <path d="{card(89, 1.0)}" fill="url(#g-card)"/>
  <path d="{rounded([(131, 73), (203, 115), (131, 157)], 13)}" fill="url(#g-play)"/>"""

DEFS = """
  <defs>
    <linearGradient id="g-card" x1="95" y1="25" x2="225" y2="190" gradientUnits="userSpaceOnUse">
      <stop offset="0" stop-color="#00B6FF"/><stop offset=".5" stop-color="#2D5BFA"/><stop offset="1" stop-color="#9A24F0"/>
    </linearGradient>
    <linearGradient id="g-ghost2" x1="49" y1="47" x2="95" y2="178" gradientUnits="userSpaceOnUse">
      <stop offset="0" stop-color="#5ED4FB"/><stop offset="1" stop-color="#A6B6FA"/>
    </linearGradient>
    <linearGradient id="g-ghost1" x1="21" y1="75" x2="50" y2="150" gradientUnits="userSpaceOnUse">
      <stop offset="0" stop-color="#C6BAF8"/><stop offset="1" stop-color="#B9B4F8"/>
    </linearGradient>
    <linearGradient id="g-play" x1="140" y1="75" x2="160" y2="160" gradientUnits="userSpaceOnUse">
      <stop offset="0" stop-color="#FFFFFF"/><stop offset=".55" stop-color="#F2F1FC"/><stop offset="1" stop-color="#B9BDF5"/>
    </linearGradient>
  </defs>"""


def wordmark(ink):
    return f"""
  <path d="{DOT}{REST}" fill="{ink}"/>
  <path d="{Q_BOWL}" fill="{ink}" fill-rule="evenodd"/>
  <path d="{Q_STEM}" fill="{ink}"/>"""


def svg(view, body, title):
    return f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="{view}" role="img" aria-label="{title}"><title>{title}</title>{DEFS}{body}\n</svg>\n'


open("sliqtly-logo.svg", "w").write(svg("12 18 624 190", ICON + wordmark("#0D1630"), "Sliqtly"))
open("sliqtly-logo-dark.svg", "w").write(svg("12 18 624 190", ICON + wordmark("#F4F5FB"), "Sliqtly"))
open("sliqtly-icon.svg", "w").write(svg("12 6 225 225", ICON, "Sliqtly"))
print("ok")
