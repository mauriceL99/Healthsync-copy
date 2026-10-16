"""Draws the HealthBridge app icon (1024 px, no alpha) with Pillow.

    python3 scripts/make-app-icon.py ios/HealthBridge/Resources/Assets.xcassets/AppIcon.appiconset/AppIcon.png
"""
import math, sys
from PIL import Image, ImageDraw, ImageFilter

S = 4096  # draw large, downscale for smooth edges
out = sys.argv[1]

# Background: diagonal teal gradient (accent color #00889A → deeper #005F73)
top, bottom = (0, 150, 168), (0, 84, 110)
bg = Image.new("RGB", (S, S))
px = bg.load()
for y in range(S):
    for x in range(0, S):
        t = (x + y) / (2 * S)
        px[x, y] = tuple(int(top[i] + (bottom[i] - top[i]) * t) for i in range(3))
img = bg
d = ImageDraw.Draw(img)

# Heart (classic parametric curve), white, centered slightly above middle
cx, cy, scale = S / 2, S * 0.48, S / 46
pts = []
for i in range(720):
    t = 2 * math.pi * i / 720
    x = 16 * math.sin(t) ** 3
    y = 13 * math.cos(t) - 5 * math.cos(2 * t) - 2 * math.cos(3 * t) - math.cos(4 * t)
    pts.append((cx + x * scale, cy - y * scale))
d.polygon(pts, fill=(255, 255, 255))

# Pulse line crossing the heart and reaching both edges (the "bridge")
w = int(S * 0.045)
line_y = cy + S * 0.02
pulse = [
    (S * 0.10, line_y), (S * 0.37, line_y), (S * 0.42, line_y - S * 0.10),
    (S * 0.50, line_y + S * 0.13), (S * 0.56, line_y - S * 0.17), (S * 0.62, line_y),
    (S * 0.90, line_y),
]
teal = (0, 118, 138)
# white outline outside the heart, teal inside: draw white wide line, then teal on top
d.line(pulse, fill=(255, 255, 255), width=w + int(S * 0.03), joint="curve")
d.line(pulse, fill=teal, width=w, joint="curve")
for (x, y) in (pulse[0], pulse[-1]):
    r = S * 0.04
    d.ellipse((x - r, y - r, x + r, y + r), fill=(255, 255, 255))

img = img.resize((1024, 1024), Image.LANCZOS).convert("RGB")  # no alpha (App Store rule)
img.save(out, "PNG", optimize=True)
print("wrote", out, img.size, img.mode)
