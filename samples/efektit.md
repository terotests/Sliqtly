---
title: Omat efektit
transition: fade
---

```fx
effect embers source {
  param speed = 1 [0, 5]
  param heat = 0.6 [0, 1]
  still = 3
  fallback = #1a0a04

  n = fbm(uv * vec2(4, 6) + vec2(0, time * 0.4 * speed), 5)
  glow = smoothstep(1 - heat, 1, n + (1 - uv.y) * 0.35)
  output = rgba(mix(#ff3d00, #ffd54f, glow), glow)
}

effect cells source {
  param scale = 60 [20, 200]
  d = voronoi(rotate(uv * size / scale, time * 6))
  output = vec4(hsv(200 + d * 120, 0.6, 0.35 + d * 0.5), 1)
}

effect northern source {
  param hue = 150 [0, 360]
  band = 1 - abs(uv.y - 0.35 - sin(uv.x * 5 + time * 0.5) * 0.08 - noise(vec2(uv.x * 3, time * 0.2)) * 0.15) * 6
  light = clamp(band, 0, 1) * (0.5 + 0.5 * noise(vec2(uv.x * 40, time)))
  sky = mix(#020617, #0b1e3f, uv.y)
  output = vec4(sky + hsv(hue + uv.x * 60, 0.8, 1) * light * 0.8, 1)
}

effect heat-haze backdrop {
  param amount = 0.004 [0, 0.03]
  q = uv + vec2(noise(uv * 18 + vec2(0, time * 2)) - 0.5, 0) * amount * 2
  output = source(q)
}

effect dawn source {
  still = 2
  fallback = #0b1026

  rise = (max(step - 1, 0) + smoothstep(0, 1.2, steptime) * min(step, 1)) / max(steps, 1)
  aspect = size.x / size.y
  sun = 1 - smoothstep(0, 0.35, distance(uv * vec2(aspect, 1), vec2(0.5 * aspect, 1.2 - rise * 0.45)))
  sky = mix(#0b1026, mix(#7c3aed, #fb923c, uv.y), rise * (0.35 + 0.65 * uv.y))
  output = vec4(sky + #ffd27a * sun * rise * 0.7, 1)
}

effect neon backdrop {
  param radius = 6 [1, 20]
  g = glow(uv, radius)
  e = edges(uv)
  output = vec4(g.rgb + #22d3ee * e * 0.6, 1)
}
```

# Omat efektit

Lohko ```fx määrittelee GPU-efektin muutamalla rivillä. Dia käyttää sitä kuin valmista: `{fx=embers}`.
{.lead}

## Hiillos {fx=embers fx-heat=0.7}

```
effect embers source {
  param heat = 0.6 [0, 1]
  n = fbm(uv * vec2(4, 6) + vec2(0, time * 0.4), 5)
  glow = smoothstep(1 - heat, 1, n + (1 - uv.y) * 0.35)
  output = rgba(mix(#ff3d00, #ffd54f, glow), glow)
}
```

## Revontulet {fx=northern}

- `source` maalaa dian taustan; teksti pysyy päällä
- `param hue = 150 [0, 360]`: dia asettaa sen `fx-hue=200`
- `uv`, `time`, `size`: missä ja milloin pikseli on

## Solut {fx=cells fx-scale=90}

`voronoi`, `noise`, `fbm`, `hsv` ja `rotate` ovat valmiina.

## Kuumuusväre {fx=heat-haze}

`backdrop`-efekti lukee valmiin dian `source(uv)`-funktiolla ja taivuttaa sitä.

- Teksti väreilee kuin ilma tien yllä
- Pikkukuvat, PDF ja PPTX näyttävät siitä stillin

## Aamunkoitto {fx=dawn}

- `step` ja `steps`: näkyvä rakennusaskel ja montako niitä on
- `steptime`: sekunnit askeleen alusta
- `progress`: 0..1 dian kestosta
{.build}

## Neon {fx=neon}

`glow(uv, r)` tuo kirkkaan ympärille hehkun, `edges(uv)` löytää ääriviivat, `blur(uv, r)` pehmentää.
