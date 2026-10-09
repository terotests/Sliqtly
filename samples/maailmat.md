---
title: 3D-maailmat kalvoilla
transition: fade
matte-label: matta
mirror-label: peili
lang: fi
---

# 3D-maailmat kalvoilla

Rangerin Three.js-portti piirtää ohjelman `<scene3d>`-elementin, ja kiiltävät pinnat heijastavat kalvon omia värejä
{.lead}

## Kalvo peilissä {art=waves}

```app
src: apps/chrome.tsx
size: 800x450
allow: 3d
```

Klikkaa pysäyttääksesi
{.kicker}

## Kaikki muodot {art=waves art-seed=7}

```app
src: apps/shapes.tsx
size: 960x300
allow: 3d
```

## Näin se kirjoitetaan

```markdown
~~~app
src: apps/chrome.tsx
size: 800x450
allow: 3d          # ohjelma saa piirtää <scene3d>-maailman
~~~
```

- `<mesh shape="knot" metal={0.9} ry={a} />`: box, sphere, torus, knot, cylinder, cone, plane, teapot
- `metal` 0–1: kuinka paljon pinta peilaa kalvoa ympärillään
- `<camera z={6} fov={40} />` ja `<light kind="sun" x y z />`, muuten valot tulevat kalvon väreistä
- Tausta on läpinäkyvä: kalvo näkyy maailman takana
